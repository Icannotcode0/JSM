package rate_limiter

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisStore keeps counters in Redis, shared across every instance and
// surviving a restart.
type redisStore struct {
	rdb *redis.Client
}

// NewRedisLimiter builds a limiter backed by Redis.
func NewRedisLimiter(rdb *redis.Client, opts ...Option) Limiter {
	return newLimiter(&redisStore{rdb: rdb}, opts...)
}

// incrScript increments a counter and sets its expiry in one atomic step.
//
// Doing this as INCR followed by EXPIRE is the obvious implementation and it is
// subtly broken: if the process dies between the two commands, the key survives
// with no TTL. That bucket is then blocked forever, for whoever happens to own
// it, and the failure is unreproducible afterwards because the race has
// already passed. A script removes the window entirely.
//
// It also returns the remaining TTL, so Retry-After comes from the same round
// trip rather than a second query that could observe a different key.
var incrScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return {count, redis.call("PTTL", KEYS[1])}
`)

func (s *redisStore) Incr(ctx context.Context, key string, window time.Duration) (int, time.Duration, error) {
	result, err := incrScript.Run(ctx, s.rdb, []string{key}, window.Milliseconds()).Slice()
	if err != nil {
		return 0, 0, fmt.Errorf("rate limiter incr: %w", err)
	}
	if len(result) != 2 {
		return 0, 0, fmt.Errorf("rate limiter incr: unexpected reply shape %v", result)
	}

	count, ok := result[0].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("rate limiter incr: count was %T", result[0])
	}
	ttlMillis, ok := result[1].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("rate limiter incr: ttl was %T", result[1])
	}

	// PTTL answers -1 for a key with no expiry and -2 when it is already gone.
	// Neither should happen given the script above, but reporting a negative
	// Retry-After would be worse than falling back to the full window.
	ttl := time.Duration(ttlMillis) * time.Millisecond
	if ttlMillis < 0 {
		ttl = window
	}

	return int(count), ttl, nil
}

func (s *redisStore) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("rate limiter del: %w", err)
	}
	return nil
}
