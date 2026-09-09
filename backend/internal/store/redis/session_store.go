package cachestore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionStore is the only place that knows Redis's session key/hash
// layout. internal/authentication's SessionManager calls this rather than holding a
// *redis.Client itself, so raw Redis commands don't leak into the
// authentication/middleware layer.
type SessionStore struct {
	rdb *redis.Client
}

func NewSessionStore(rdb *redis.Client) *SessionStore {
	return &SessionStore{rdb: rdb}
}

// Create writes the session hash for sid and sets its TTL.
func (s *SessionStore) Create(ctx context.Context, sid string, fields map[string]any, ttl time.Duration) error {
	key := "session:" + sid
	if err := s.rdb.HSet(ctx, key, fields).Err(); err != nil {
		return err
	}
	return s.rdb.Expire(ctx, key, ttl).Err()
}

// Get returns the session hash for sid (empty map if it doesn't exist).
func (s *SessionStore) Get(ctx context.Context, sid string) (map[string]string, error) {
	return s.rdb.HGetAll(ctx, "session:"+sid).Result()
}

// Delete removes the session hash for sid.
func (s *SessionStore) Delete(ctx context.Context, sid string) error {
	return s.rdb.Del(ctx, "session:"+sid).Err()
}
