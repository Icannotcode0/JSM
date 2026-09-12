// Package rate_limiter throttles abusive traffic before it reaches the
// expensive part of a request.
//
// The endpoints that need it are: login, signup, change-password, which all run bcrypt,
// which is the tell: a limiter that runs *after* the hash protects accounts but
// not the CPU, and CPU exhaustion is half of what an attacker is buying. Every
// check therefore happens before the work, never after.
//
// What is deliberately NOT limited: GET /health. The frontend calls it to obtain
// a CSRF token when the cookie is missing, so throttling it breaks login for
// exactly the users who retry most. It is also the liveness probe.
package rate_limiter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Scope and key-type identifiers. Kept as constants because they end up inside
// Redis keys — a typo would silently create a second, empty bucket rather than
// fail.
const (
	ScopeLogin         = "login"
	ScopeSignup        = "signup"
	ScopeResetPassword = "reset_password"

	KeyIP    = "ip"
	KeyEmail = "email"
	KeyUser  = "user"
)

// Key identifies one bucket: what is being limited, and for whom.
type Key struct {
	Scope string // ScopeLogin, ScopeSignup, ...
	Type  string // KeyIP, KeyEmail, KeyUser
	Value string // the client IP, normalised email, or user ID
}

// Rule is one limit. A policy may hold several, and all of them must pass.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Decision is the answer to "may this request proceed".
type Decision struct {
	Allowed bool

	// RetryAfter is how long until the exhausted bucket refills. Zero when allowed.
	RetryAfter time.Duration
}

type Limiter interface {
	Allow(ctx context.Context, keys ...Key) (Decision, error)
	Reset(ctx context.Context, keys ...Key) error
}

type store interface {
	Incr(ctx context.Context, key string, window time.Duration) (count int, ttl time.Duration, err error)
	Del(ctx context.Context, keys ...string) error
}

// Policy maps a scope and key type to the rules that govern it.
//
// Two tiers per entry, not one: a single window is trivially paced around —
// exhaust it, wait for the boundary, repeat. A short burst rule plus a long
// sustained rule means an attacker slow enough to defeat the first runs into
// the second.
type Policy map[string][]Rule

func policyKey(scope, keyType string) string { return scope + ":" + keyType }

func DefaultPolicy() Policy {
	return Policy{
		policyKey(ScopeLogin, KeyIP): {
			{Limit: 10, Window: time.Minute},
			{Limit: 60, Window: time.Hour},
		},
		policyKey(ScopeLogin, KeyEmail): {
			{Limit: 5, Window: 5 * time.Minute},
			{Limit: 20, Window: time.Hour},
		},
		policyKey(ScopeSignup, KeyIP): {
			{Limit: 3, Window: 10 * time.Minute},
			{Limit: 10, Window: 24 * time.Hour},
		},
		policyKey(ScopeResetPassword, KeyUser): {
			{Limit: 5, Window: 15 * time.Minute},
		},
	}
}

type limiter struct {
	store  store
	policy Policy
	now    func() time.Time
}

// Option customises a limiter at construction.
type Option func(*limiter)

// WithPolicy replaces the default limits.
func WithPolicy(p Policy) Option {
	return func(l *limiter) { l.policy = p }
}

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option {
	return func(l *limiter) { l.now = now }
}

func newLimiter(s store, opts ...Option) *limiter {
	l := &limiter{store: s, policy: DefaultPolicy(), now: time.Now}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func (l *limiter) Allow(ctx context.Context, keys ...Key) (Decision, error) {
	decision := Decision{Allowed: true}

	for _, key := range keys {
		rules := l.policy[policyKey(key.Scope, key.Type)]
		if len(rules) == 0 {
			// No policy for this scope and key type means "not limited". A
			// missing entry is a configuration choice, not an error — it is how
			// an endpoint opts out.
			continue
		}

		for _, rule := range rules {
			count, ttl, err := l.store.Incr(ctx, l.bucket(key, rule), rule.Window)
			if err != nil {
				return Decision{Allowed: false, RetryAfter: rule.Window}, err
			}

			if count > rule.Limit {
				decision.Allowed = false
				// Report the longest wait across every tripped rule. Telling a
				// caller to retry in 30s while a one-hour bucket is also
				// exhausted just invites another rejected request.
				if ttl > decision.RetryAfter {
					decision.RetryAfter = ttl
				}
			}
		}
	}

	return decision, nil
}

func (l *limiter) Reset(ctx context.Context, keys ...Key) error {
	var names []string
	for _, key := range keys {
		for _, rule := range l.policy[policyKey(key.Scope, key.Type)] {
			names = append(names, l.bucket(key, rule))
		}
	}
	if len(names) == 0 {
		return nil
	}
	return l.store.Del(ctx, names...)
}

// bucket builds the storage key for one rule.
//
// The window start is part of the key, which is what makes this a fixed-window
// counter: each window is a distinct key that expires on its own, so nothing
// ever has to be swept.
//
// The value is hashed rather than embedded. Keys hold email addresses and IPs,
// and a limiter should not turn the session store into a list of every address
// anyone has ever typed. Truncating to 128 bits keeps keys short while leaving
// collisions irrelevant at this cardinality.
func (l *limiter) bucket(key Key, rule Rule) string {
	sum := sha256.Sum256([]byte(key.Value))
	windowStart := l.now().Truncate(rule.Window).Unix()

	return fmt.Sprintf("rl:%s:%s:%s:%d:%d",
		key.Scope, key.Type, hex.EncodeToString(sum[:16]), int64(rule.Window.Seconds()), windowStart)
}
