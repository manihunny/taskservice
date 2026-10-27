package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"taskservice/internal/domain"
)

const taskCols = "id, team_id, title, description, status, created_by, assignee_id, created_at, updated_at, closed_at, version"

type scanner interface{ Scan(dest ...any) error }

func scanTask(s scanner) (*domain.Task, error) {
	var (
		t        domain.Task
		assignee sql.NullInt64
		closed   sql.NullTime
	)
	err := s.Scan(&t.ID, &t.TeamID, &t.Title, &t.Description, &t.Status, &t.CreatedBy,
		&assignee, &t.CreatedAt, &t.UpdatedAt, &closed, &t.Version)
	if err != nil {
		return nil, err
	}
	if assignee.Valid {
		t.AssigneeID = &assignee.Int64
	}
	if closed.Valid {
		t.ClosedAt = &closed.Time
	}
	return &t, nil
}

func (r *Repo) InsertTask(ctx context.Context, t *domain.Task) error {
	res, err := r.q.ExecContext(ctx, `
		INSERT INTO tasks (team_id, title, description, status, created_by, assignee_id, created_at, updated_at, closed_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TeamID, t.Title, t.Description, t.Status, t.CreatedBy, t.AssigneeID,
		t.CreatedAt, t.UpdatedAt, t.ClosedAt, t.Version)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// GetTask с forUpdate берёт строковую блокировку и должен вызываться внутри транзакции.
func (r *Repo) GetTask(ctx context.Context, id int64, forUpdate bool) (*domain.Task, error) {
	q := "SELECT " + taskCols + " FROM tasks WHERE id = ?"
	if forUpdate {
		q += " FOR UPDATE"
	}
	t, err := scanTask(r.q.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// UpdateTask дополнительно сверяет версию, чтобы не перезаписать чужое изменение.
func (r *Repo) UpdateTask(ctx context.Context, t *domain.Task, expectedVersion int64) error {
	res, err := r.q.ExecContext(ctx, `
		UPDATE tasks
		SET title = ?, description = ?, status = ?, assignee_id = ?, updated_at = ?, closed_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		t.Title, t.Description, t.Status, t.AssigneeID, t.UpdatedAt, t.ClosedAt, t.ID, expectedVersion)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrVersionConflict
	}
	t.Version = expectedVersion + 1
	return nil
}

func (r *Repo) ListTasks(ctx context.Context, f domain.TaskFilter) ([]domain.Task, int, error) {
	where := []string{"team_id = ?"}
	args := []any{f.TeamID}
	if f.Status != nil {
		where = append(where, "status = ?")
		args = append(args, *f.Status)
	}
	if f.AssigneeID != nil {
		where = append(where, "assignee_id = ?")
		args = append(args, *f.AssigneeID)
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.QueryContext(ctx,
		"SELECT "+taskCols+" FROM tasks WHERE "+cond+" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?",
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *t)
	}
	return items, total, rows.Err()
}

func (r *Repo) InsertHistory(ctx context.Context, taskID, changedBy int64, changes domain.Changes, at time.Time) error {
	body, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx,
		"INSERT INTO task_history (task_id, changed_by, changes, created_at) VALUES (?, ?, ?, ?)",
		taskID, changedBy, body, at)
	return err
}

func (r *Repo) ListHistory(ctx context.Context, taskID int64) ([]domain.HistoryEntry, error) {
	rows, err := r.q.QueryContext(ctx,
		"SELECT id, task_id, changed_by, changes, created_at FROM task_history WHERE task_id = ? ORDER BY id", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := []domain.HistoryEntry{}
	for rows.Next() {
		var h domain.HistoryEntry
		var changes []byte
		if err := rows.Scan(&h.ID, &h.TaskID, &h.ChangedBy, &changes, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Changes = changes
		res = append(res, h)
	}
	return res, rows.Err()
}

func (r *Repo) InsertComment(ctx context.Context, c *domain.Comment) error {
	res, err := r.q.ExecContext(ctx,
		"INSERT INTO task_comments (task_id, user_id, content, created_at) VALUES (?, ?, ?, ?)",
		c.TaskID, c.UserID, c.Content, c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (r *Repo) ListComments(ctx context.Context, taskID int64) ([]domain.Comment, error) {
	rows, err := r.q.QueryContext(ctx,
		"SELECT id, task_id, user_id, content, created_at FROM task_comments WHERE task_id = ? ORDER BY id", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.UserID, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		res = append(res, c)
	}
	return res, rows.Err()
}
