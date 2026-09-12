package rate_limiter

import (
	"context"
	"sync"
	"time"
)

// memoryStore keeps counters in process memory.
//
// Two uses. Tests get a limiter with no database, which is what keeps the
// handler suite fast. And a single-instance deployment with no Redis still gets
// real limiting rather than none.
//
// It is explicitly not right for more than one instance: each process would
// hold its own counters, so the effective limit multiplies by the instance
// count. Use redisStore there.
type memoryStore struct {
	mu      sync.Mutex
	buckets map[string]*memoryBucket
	now     func() time.Time
}

type memoryBucket struct {
	count     int
	expiresAt time.Time
}

// NewMemoryLimiter builds a limiter backed by process memory.
func NewMemoryLimiter(opts ...Option) Limiter {
	s := &memoryStore{buckets: map[string]*memoryBucket{}, now: time.Now}

	l := newLimiter(s, opts...)
	// The store shares the limiter's clock so a test that advances time moves
	// expiry and window boundaries together. Without this, WithClock would
	// shift which bucket is addressed while leaving old buckets alive.
	s.now = l.now
	return l
}

func (s *memoryStore) Incr(_ context.Context, key string, window time.Duration) (int, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweep(now)

	bucket, ok := s.buckets[key]
	if !ok || !bucket.expiresAt.After(now) {
		bucket = &memoryBucket{expiresAt: now.Add(window)}
		s.buckets[key] = bucket
	}
	bucket.count++

	return bucket.count, bucket.expiresAt.Sub(now), nil
}

func (s *memoryStore) Del(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, key := range keys {
		delete(s.buckets, key)
	}
	return nil
}

// sweep drops expired buckets.
//
// Redis expires keys for us; in memory nothing does, and every window creates a
// fresh key. Without this the map grows for the life of the process — one entry
// per distinct address per window — which is a slow leak an attacker can
// accelerate simply by varying their source address.
func (s *memoryStore) sweep(now time.Time) {
	for key, bucket := range s.buckets {
		if !bucket.expiresAt.After(now) {
			delete(s.buckets, key)
		}
	}
}
