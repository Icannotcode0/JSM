package handlers

import (
	"errors"
	"net/http"

	"github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
)

type userHandler struct {
	users service.UserReader
}

func NewUserHandler(users service.UserReader) *userHandler {

	return &userHandler{users: users}
}

// Me implements GET /me: 200 {"user": {...}}.
func (h *userHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := authentication.UserIDFromContext(r.Context())
	if !ok {
		jsmHttp.WriteJSONError(w, metrics.CodeUnauthorized, http.StatusUnauthorized)
		return
	}

	user, err := h.users.Me(r.Context(), userID)
	if err != nil {
		if errors.Is(err, metrics.ErrUserNotFound) {
			// The session is valid but its user is gone (deleted account, or a
			// database restored from an older snapshot). That's not a server
			// fault — it means this session is no longer usable.
			jsmHttp.WriteJSONError(w, metrics.CodeUnauthorized, http.StatusUnauthorized)
			return
		}
		logbuilder.NewDefaultInfoLevelLogger().
			Error("[Me]: lookup failed", logbuilder.Fields{"error": err.Error()})
		jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
		return
	}

	jsmHttp.WriteJSON(w, map[string]any{"user": user}, http.StatusOK)
}
