package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
)

// RecoverMiddleware catches a panic anywhere downstream in the handler
// chain and turns it into a clean JSON 500 instead of an abruptly closed
// connection.
func RecoverMiddleware() func(http.Handler) http.Handler {
	// Built once, at router-setup time — not per-request.
	logLevel := logbuilder.LogBuilderInfoLevel
	disableColors := true
	lb, err := logbuilder.NewJsmLogger(logbuilder.Config{
		Level:         &logLevel,
		DisableColors: &disableColors,
	})
	if err != nil {
		// Logging is best-effort here: a broken logger shouldn't take down
		// request handling, so fall back to not logging rather than 500ing
		// every request.
		lb = nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if lb != nil {
						lb.Error("panic recovered", logbuilder.Fields{
							"error":  fmt.Sprintf("%v", rec),
							"stack":  string(debug.Stack()),
							"path":   r.URL.Path,
							"method": r.Method,
						})
					}
					// Stack trace and panic value stay server-side only —
					// the client gets a generic message, never internals.
					jsmHttp.WriteJSONError(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
