package handlers

import (
	"net/http"

	"github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
)

// Depends on the single capability it uses rather than *service.Services, so
// the health endpoint can be tested with a one-method fake.
type healthHandler struct {
	health service.HealthChecker
}

func NewHealthHandler(health service.HealthChecker) *healthHandler {
	return &healthHandler{health: health}
}

// HealthCheck implements GET /health (API.md): 200 {"status":"ok"} when every
// backing store answers, 503 otherwise.
func (h *healthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if err := h.health.HealthCheck(r.Context()); err != nil {
		log := logbuilder.NewDefaultInfoLevelLogger()
		log.Error("health check failed", logbuilder.Fields{"error": err.Error()})

		// 503, not 500: the service itself is fine, its dependencies aren't —
		// and 503 is what probes and load balancers act on.
		jsmHttp.WriteJSONError(w, metrics.CodeServiceUnavailable, http.StatusServiceUnavailable)
		return
	}

	jsmHttp.WriteJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}
