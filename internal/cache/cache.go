package cache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"taskservice/internal/domain"
)

// TaskCache кеширует страницы списка задач. Инвалидация через версию команды:
// INCR версии делает все старые ключи недостижимыми, они доживают по TTL. Без SCAN/KEYS.
// Ошибки Redis не валят запрос: сервис уходит в БД.
type TaskCache struct {
	rdb *redis.Client
	ttl time.Duration
	log *slog.Logger
}

func New(rdb *redis.Client, ttl time.Duration, log *slog.Logger) *TaskCache {
	return &TaskCache{rdb: rdb, ttl: ttl, log: log}
}

func versionKey(teamID int64) string { return fmt.Sprintf("tasks:team:%d:ver", teamID) }

// Version нужно читать до запроса в БД: тогда данные, прочитанные до инвалидации,
// сохранятся под старой версией и не будут отданы.
func (c *TaskCache) Version(ctx context.Context, teamID int64) (int64, bool) {
	v, err := c.rdb.Get(ctx, versionKey(teamID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, true
	}
	if err != nil {
		c.log.WarnContext(ctx, "cache version read failed", "err", err, "team_id", teamID)
		return 0, false
	}
	return v, true
}

func ListKey(f domain.TaskFilter, version int64) string {
	status, assignee := "", ""
	if f.Status != nil {
		status = string(*f.Status)
	}
	if f.AssigneeID != nil {
		assignee = fmt.Sprint(*f.AssigneeID)
	}
	sum := sha1.Sum(fmt.Appendf(nil, "status=%s&assignee=%s&limit=%d&offset=%d", status, assignee, f.Limit, f.Offset))
	return fmt.Sprintf("tasks:team:%d:v%d:%s", f.TeamID, version, hex.EncodeToString(sum[:8]))
}

func (c *TaskCache) Get(ctx context.Context, key string) ([]byte, bool) {
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.log.WarnContext(ctx, "cache get failed", "err", err)
		}
		return nil, false
	}
	return b, true
}

func (c *TaskCache) Set(ctx context.Context, key string, val []byte) {
	if err := c.rdb.Set(ctx, key, val, c.ttl).Err(); err != nil {
		c.log.WarnContext(ctx, "cache set failed", "err", err)
	}
}

func (c *TaskCache) Invalidate(ctx context.Context, teamID int64) {
	if err := c.rdb.Incr(ctx, versionKey(teamID)).Err(); err != nil {
		c.log.ErrorContext(ctx, "cache invalidate failed", "err", err, "team_id", teamID)
	}
}
