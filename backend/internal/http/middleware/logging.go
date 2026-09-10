package middleware

import (
	"net/http"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
)

func LoggingMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeTicker := time.Now()
			initLogger := logbuilder.NewDefaultInfoLevelLogger()
			initLogger.Info("Init Request", logbuilder.Fields{
				"timestamp":  time.Now(),
				"method":     r.Method,
				"path":       r.URL.Path,
				"RemoteAddr": r.RemoteAddr,
			})
			next.ServeHTTP(w, r)
			initLogger.Info("Finish Request", logbuilder.Fields{
				"elapsed": time.Since(timeTicker),
			})
		})
	}
}
