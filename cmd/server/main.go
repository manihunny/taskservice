package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"taskservice/internal/api"
	"taskservice/internal/auth"
	"taskservice/internal/cache"
	"taskservice/internal/config"
	"taskservice/internal/service"
	"taskservice/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.MySQLDSN, 60*time.Second)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := store.Migrate(ctx, cfg.MySQLDSN); err != nil {
		return err
	}
	log.Info("migrations applied")

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		// Без Redis сервис работает, просто без кеша
		log.Warn("redis unavailable, cache disabled until it recovers", "err", err)
	}

	svc := service.New(st, cache.New(rdb, cfg.CacheTTL, log), auth.NewJWT(cfg.JWTSecret, cfg.JWTTTL), log)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(ctx, svc, log, cfg.RateLimitRPS, cfg.RateLimitBurst, st.Ping),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server started", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down")
	return srv.Shutdown(shutdownCtx)
}
