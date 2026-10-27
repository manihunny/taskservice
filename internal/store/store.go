package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/go-sql-driver/mysql"

	"taskservice/migrations"
)

type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Repo работает и поверх пула, и поверх транзакции.
type Repo struct {
	q dbtx
}

type Store struct {
	*Repo
	db *sql.DB
}

// Open ждёт готовности MySQL: в docker compose приложение может стартовать раньше БД.
func Open(ctx context.Context, dsn string, wait time.Duration) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	deadline := time.Now().Add(wait)
	for {
		err = db.PingContext(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = db.Close()
			return nil, fmt.Errorf("mysql ping: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = db.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return &Store{Repo: &Repo{q: db}, db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) InTx(ctx context.Context, fn func(r *Repo) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(&Repo{q: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// Migrate применяет встроенные SQL-миграции по порядку имён файлов.
func Migrate(ctx context.Context, dsn string) error {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return err
	}
	// multiStatements включаем только для отдельного соединения миграций
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Блокировка защищает от параллельных миграций при нескольких репликах
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK('taskservice_migrate', 60)").Scan(&locked); err != nil {
		return err
	}
	if locked.Int64 != 1 {
		return errors.New("migrate: could not acquire lock")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('taskservice_migrate')")

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at DATETIME(6)  NOT NULL
	)`); err != nil {
		return err
	}

	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		var exists int
		err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", f).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrations.FS.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", f, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", f, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}
