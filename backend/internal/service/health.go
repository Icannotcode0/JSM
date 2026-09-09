package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
	"github.com/redis/go-redis/v9"
)

// healthTimeout caps how long a health check may run. A health endpoint that
// can hang is worse than one that reports a failure: liveness probes read a
// timeout as "no answer" rather than "down".
const healthTimeout = 10 * time.Second

type health struct {
	store   *store.Store
	session *redis.Client
}

func NewHealth(s *store.Store, rdsClient *redis.Client) *health {
	return &health{
		store:   s,
		session: rdsClient,
	}
}

// HealthCheck pings every backing store and reports which ones failed.
//
// Both checks always run — an early return on the first failure would report
// Mongo being down while saying nothing about Redis, so a single request could
// never tell you the whole system state.
func (h *health) HealthCheck(ctx context.Context) error {
	// Derived from the caller's context, not context.Background(): a client
	// that disconnects mid-check cancels the pings instead of leaving them to
	// run against a response nobody will read.
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	var mongoErr, redisErr error
	if err := h.store.Health.CheckHealth(ctx); err != nil {
		mongoErr = fmt.Errorf("mongo: %w", err)
	}
	if err := h.session.Ping(ctx).Err(); err != nil {
		redisErr = fmt.Errorf("redis: %w", err)
	}

	// errors.Join drops nils and returns nil when every check passed, so the
	// healthy path needs no special case.
	return errors.Join(mongoErr, redisErr)
}
