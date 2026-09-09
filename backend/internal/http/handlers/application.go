package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
)

type applicationHandler struct {
	applications service.ApplicationService
}

func NewApplicationHandler(applications service.ApplicationService) *applicationHandler {
	return &applicationHandler{applications: applications}
}

// List implements GET /applications, with optional status / tag / q filters and
// page / page_size paging.
func (h *applicationHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	query := domain.ListApplicationsQuery{
		Status:   r.URL.Query().Get("status"),
		Tag:      r.URL.Query().Get("tag"),
		Search:   r.URL.Query().Get("q"),
		Page:     atoiOr(r.URL.Query().Get("page"), 1),
		PageSize: atoiOr(r.URL.Query().Get("page_size"), 0),
	}

	page, err := h.applications.List(r.Context(), userID, query)
	if err != nil {
		writeServiceError(w, "List", err)
		return
	}

	// The page envelope is returned flat rather than nested under a key, so it
	// matches the {applications, page, page_size, total} shape in API.md.
	jsmHttp.WriteJSON(w, page, http.StatusOK)
}

// Get implements GET /applications/{id}.
func (h *applicationHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	app, err := h.applications.Get(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, "Get", err)
		return
	}
	jsmHttp.WriteJSON(w, map[string]any{"application": app}, http.StatusOK)
}

// Create implements POST /applications.
func (h *applicationHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	jsmHttp.LimitBody(w, r)

	var req domain.CreateApplicationRequest
	if err := jsmHttp.Decode(r.Context(), r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	app, err := h.applications.Create(r.Context(), userID, req)
	if err != nil {
		writeServiceError(w, "Create", err)
		return
	}
	jsmHttp.WriteJSON(w, map[string]any{"application": app}, http.StatusCreated)
}

// Update implements PATCH /applications/{id}. Only the fields present in the
// body are changed.
func (h *applicationHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	jsmHttp.LimitBody(w, r)

	var req domain.UpdateApplicationRequest
	if err := jsmHttp.Decode(r.Context(), r.Body, &req); err != nil {
		writeDecodeError(w, err)
		return
	}

	app, err := h.applications.Update(r.Context(), userID, r.PathValue("id"), req)
	if err != nil {
		writeServiceError(w, "Update", err)
		return
	}
	jsmHttp.WriteJSON(w, map[string]any{"application": app}, http.StatusOK)
}

// Delete implements DELETE /applications/{id}: 204, no body.
func (h *applicationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	if err := h.applications.Delete(r.Context(), userID, r.PathValue("id")); err != nil {
		writeServiceError(w, "Delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* -------------------------------------------------------------------------
   Shared helpers
   ------------------------------------------------------------------------- */

// requireUser pulls the authenticated user off the context, refusing the
// request if it isn't there. SessionRequired guarantees it for correctly
// mounted routes; this makes a routing mistake fail closed rather than serve
// an unauthenticated caller.
func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := authentication.UserIDFromContext(r.Context())
	if !ok {
		jsmHttp.WriteJSONError(w, metrics.ErrUnauthorized, http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func atoiOr(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	// A malformed page number falls back to the default rather than erroring:
	// paging is a display concern, and failing the whole request over "page=x"
	// is worse than showing page 1. The service bounds the result either way.
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.Is(err, jsmHttp.ErrBodyTooLarge) || errors.As(err, &maxBytes) {
		jsmHttp.WriteJSONError(w, metrics.ErrRequestTooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	jsmHttp.WriteJSONError(w, metrics.ErrBadRequest, http.StatusBadRequest)
}

// writeServiceError maps a service error onto a status code.
//
// Only ErrInvalidInput's message reaches the client — every string in it is
// written in the service, never interpolated from the database or the driver.
// Anything unrecognised is a 500 with a generic body and the detail in the log,
// so an internal failure can't describe our schema to a caller.
func writeServiceError(w http.ResponseWriter, op string, err error) {
	var invalid service.ErrInvalidInput

	switch {
	case errors.As(err, &invalid):
		jsmHttp.WriteJSONError(w, invalid.Reason, http.StatusBadRequest)
	case errors.Is(err, service.ErrApplicationNotFound):
		jsmHttp.WriteJSONError(w, metrics.ErrNotFound, http.StatusNotFound)
	default:
		logbuilder.NewDefaultInfoLevelLogger().
			Error("[applications."+op+"]: failed", logbuilder.Fields{"error": err.Error()})
		jsmHttp.WriteJSONError(w, metrics.ErrInternalServerError, http.StatusInternalServerError)
	}
}
