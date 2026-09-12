package rate_limiter

import (
	"net/http"
	"strconv"
	"time"

	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
)

// Guard is what handlers are given.
//
// It pairs a Limiter with a ClientIPResolver because neither is useful alone: a
// limit needs something to key on, and the address is only ever derived for
// this purpose. Handlers receive this, not the pieces.
type Guard interface {
	// Middleware limits a route by client address before the handler runs.
	//
	// Suitable wherever the address is the whole key. Login also needs a
	// per-email limit, and that key only exists after the body is decoded — so
	// the handler calls Allow directly instead. See AllowRequest.
	Middleware(scope string) func(http.Handler) http.Handler

	// AllowRequest consumes the client-address bucket for scope, plus any
	// additional keys the handler can supply once it has parsed the request.
	//
	// Returns true when the request may proceed. On false it has already
	// written 429 and the handler must return immediately.
	AllowRequest(w http.ResponseWriter, r *http.Request, scope string, extra ...Key) bool

	// ResetRequest clears the buckets for a request that succeeded, so somebody
	// who mistyped their password twice before getting it right is not left one
	// attempt from a lockout.
	ResetRequest(r *http.Request, scope string, extra ...Key)

	// ClientKey builds the address key for a scope, for callers that need it
	// without going through AllowRequest.
	ClientKey(r *http.Request, scope string) Key
}

type guard struct {
	limiter  Limiter
	resolver ClientIPResolver
}

// NewGuard wires a limiter to an address resolver.
func NewGuard(l Limiter, resolver ClientIPResolver) Guard {
	return &guard{limiter: l, resolver: resolver}
}

func (g *guard) ClientKey(r *http.Request, scope string) Key {
	return Key{Scope: scope, Type: KeyIP, Value: g.resolver.BucketValue(r)}
}

func (g *guard) Middleware(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !g.AllowRequest(w, r, scope) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (g *guard) AllowRequest(w http.ResponseWriter, r *http.Request, scope string, extra ...Key) bool {
	keys := append([]Key{g.ClientKey(r, scope)}, extra...)

	decision, err := g.limiter.Allow(r.Context(), keys...)
	if err != nil {
		// Fail closed, loudly. A limiter that silently stops limiting when its
		// store is unreachable is worse than no limiter, because nothing
		// reveals that it has stopped.
		logbuilder.NewDefaultInfoLevelLogger().Error("rate limiter unavailable", logbuilder.Fields{
			"scope": scope,
			"error": err.Error(),
		})
		writeTooManyRequests(w, decision.RetryAfter)
		return false
	}

	if !decision.Allowed {
		// Key *types*, never values. Tuning these limits needs to know which
		// rule trips and how often; it does not need a log full of the email
		// addresses and IPs of everyone who mistyped a password.
		types := make([]string, 0, len(keys))
		for _, k := range keys {
			types = append(types, k.Type)
		}
		logbuilder.NewDefaultInfoLevelLogger().Warn("rate limit exceeded", logbuilder.Fields{
			"scope":       scope,
			"keys":        types,
			"retry_after": decision.RetryAfter.String(),
		})
		writeTooManyRequests(w, decision.RetryAfter)
		return false
	}

	return true
}

func (g *guard) ResetRequest(r *http.Request, scope string, extra ...Key) {
	keys := append([]Key{g.ClientKey(r, scope)}, extra...)

	if err := g.limiter.Reset(r.Context(), keys...); err != nil {
		// A failed reset costs the user some of their allowance; it does not
		// cost them the request they just completed successfully. Log and move
		// on rather than failing a request that already worked.
		logbuilder.NewDefaultInfoLevelLogger().Warn("rate limiter reset failed", logbuilder.Fields{
			"scope": scope,
			"error": err.Error(),
		})
	}
}

// writeTooManyRequests answers identically no matter which rule tripped.
//
// Saying "too many attempts for this account" rather than "from this address"
// would confirm that an account exists — the same enumeration oracle closed by
// spending equal time on unknown emails, reopened through a different door.
// Retry-After may vary, because a duration reveals nothing.
func writeTooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	jsmHttp.WriteJSONError(w, metrics.CodeTooManyRequests, http.StatusTooManyRequests)
}
