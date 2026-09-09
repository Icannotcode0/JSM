package redisWrap

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"time"

	authen "github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/config"
	cachestore "github.com/Icannotcode0/job-app-manager/backend/internal/store/redis"
	"github.com/redis/go-redis/v9"
)

// Redirect cookie policy isn't in config.CookieConfig (unlike the session and
// CSRF cookie names), so it's defaulted here. Promote these to config if they
// ever need to differ per environment.
const (
	defaultRedirectCookieName = "jsm_redirect"
	defaultRedirectTTL        = 10 * time.Minute
)

// dialTimeout bounds the startup Ping only. It is deliberately short: if Redis
// isn't up, failing fast at boot beats a server that accepts requests and only
// discovers it has no session store on the first login.
const dialTimeout = 3 * time.Second

// NewClient connects to Redis and verifies the connection before returning,
// mirroring mongoWrap.NewClient. A returned client is known-reachable; on any
// error the half-open client is closed rather than leaked to the caller.
func NewClient(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	pingCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// NewSessionManager builds the SessionManager from config: it wraps rdb in the
// SessionStore that owns the Redis key layout, then hands that plus cookie
// policy to authentication.NewSessionManager.
//
// Its only job is to turn config into constructor arguments, so it takes an
// already-connected client rather than dialing its own — main.go needs the same
// *redis.Client for the health service, and two clients would mean the health
// check pings a connection nothing else uses.
func NewSessionManager(rdb *redis.Client, cookie config.CookieConfig) (*authen.SessionManager, error) {
	secret, err := csrfSecret(cookie.Secret)
	if err != nil {
		return nil, err
	}

	return authen.NewSessionManager(
		cachestore.NewSessionStore(rdb),
		cookie.TTL,
		cookie.SessionCookieName,
		cookie.CSRFCookieName,
		defaultRedirectCookieName,
		defaultRedirectTTL,
		authen.CookieSettings{
			Domain: cookie.Domain,
			Secure: cookie.Secure,
		},
		secret,
	), nil
}

// csrfSecret returns the HMAC key for signing CSRF tokens.
//
// SESSION_SECRET is empty by default, and starting with an empty HMAC key would
// mean any attacker who guessed that could mint valid tokens — the exact
// forgery the signature exists to stop. So an unset secret becomes a random one
// generated at boot: safe by default, at the cost of invalidating outstanding
// CSRF tokens on restart (clients recover on their next safe request). Set
// SESSION_SECRET to keep them stable across restarts.
func csrfSecret(configured string) ([]byte, error) {
	if configured != "" {
		return []byte(configured), nil
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate CSRF secret: %w", err)
	}
	log.Print("redisWrap: SESSION_SECRET unset — using a random per-boot CSRF secret")
	return secret, nil
}
