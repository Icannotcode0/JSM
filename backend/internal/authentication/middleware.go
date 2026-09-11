package authentication

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
)

// ctxKey is an unexported type so context values this package sets can never
// collide with a same-named key from another package.
type ctxKey int

const userIDKey ctxKey = iota

// UserIDFromContext returns the authenticated user's ID stored by
// SessionRequired, and whether one was present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok
}

// ContextWithUserID is the write half of UserIDFromContext.
//
// SessionRequired is the only production caller — it exists as an exported
// function so handler tests can build a request that looks authenticated
// without standing up Redis and a real session. The key stays unexported, so
// nothing outside this package can forge the value by other means.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// CSRFMiddleWare implements the double-submit-cookie pattern: safe methods
// ensure a CSRF cookie is present (issuing one if missing) and pass straight
// through — no proof is required to *receive* a token; unsafe methods
// require the cookie value and an X-CSRF-TOKEN header to match.
func CSRFMiddleWare(sm *SessionManager) func(http.Handler) http.Handler {
	// Built once, at router-setup time — not per-request.
	logLevel := logbuilder.LogBuilderInfoLevel
	disableColors := true
	lb, err := logbuilder.NewJsmLogger(logbuilder.Config{
		Level:         &logLevel,
		DisableColors: &disableColors,
	})
	if err != nil {
		lb = nil
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || r.Method == http.MethodHead || r.Method == http.MethodGet {
				if _, err := sm.EnsureCSRFToken(w, r); err != nil {
					jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			csrfCookie, err := r.Cookie(sm.CsrfCookieName)
			if err != nil || csrfCookie.Value == "" {
				jsmHttp.WriteJSONError(w, "csrf token missing", http.StatusForbidden)
				return
			}
			csrfHeader := r.Header.Get(metrics.FCSRFTOKEN)
			if csrfHeader == "" {
				jsmHttp.WriteJSONError(w, "csrf token missing", http.StatusForbidden)
				return
			}

			if subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(csrfHeader)) != 1 {
				jsmHttp.WriteJSONError(w, "csrf token mismatch", http.StatusForbidden)
				return
			}

			// The match above only proves the two halves agree — an attacker who
			// can write the cookie chooses both. This is the check that matters:
			// the token must carry this server's signature and be bound to the
			// session presenting it. See csrf.go for why a localhost service
			// can't rely on the cookie being unwritable.
			if !sm.VerifyCSRFToken(csrfCookie.Value, sm.SessionIDFromRequest(r)) {
				jsmHttp.WriteJSONError(w, "csrf token invalid", http.StatusForbidden)
				return
			}

			if lb != nil {
				lb.Info("client CSRF passed", logbuilder.Fields{
					"remote_addr": r.RemoteAddr,
					"method":      r.Method,
				})
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SessionRequired rejects requests without a valid session cookie, and
// stores the authenticated user's ID on the request context (read it back
// with UserIDFromContext) for downstream handlers.
func SessionRequired(sm *SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessCookie, err := r.Cookie(sm.SessionCookieName)
			if err != nil || sessCookie.Value == "" {
				jsmHttp.WriteJSONError(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			userID, err := sm.GetUserIDFromRequest(r.Context(), sessCookie.Value)
			if err != nil || userID == "" {
				jsmHttp.WriteJSONError(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// UserId seeded into every session-required endpoint at this middleware
			ctx := ContextWithUserID(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
