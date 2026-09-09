package handlers

import (
	"errors"
	"net/http"
	"time"

	jsmHttp "github.com/Icannotcode0/job-app-manager/backend/internal/common/jsmHttp"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/domain"
	"github.com/Icannotcode0/job-app-manager/backend/internal/service"
)

// SessionReader extracts the presented session ID from a request.
//
// Logout can't use UserIDFromContext the way the other authenticated handlers
// do: it is mounted outside SessionRequired, precisely so that logging out
// without a session answers 200 instead of 401. So it reads the raw cookie.
// Declared as a one-method interface here, at the consumer, rather than taking
// the whole *SessionManager — this handler has no business minting sessions.
type SessionReader interface {
	SessionIDFromRequest(r *http.Request) string
}

// Mirrors healthHandler: depend on the one capability used, not the whole
// Services aggregate. This also drops the by-value copy the previous signature
// made on every construction.
type auth struct {
	authenticator service.Authenticator
	sessions      SessionReader
}

func NewAuth(authenticator service.Authenticator, sessions SessionReader) *auth {
	return &auth{
		authenticator: authenticator,
		sessions:      sessions,
	}
}

// Logout implements POST /logout: 200 {"status":"ok"}, always.
//
// Succeeding when there was no session to end is intentional (API.md) — the
// caller asked to be signed out, and they are.
func (a *auth) Logout(w http.ResponseWriter, r *http.Request) {
	cookies, err := a.authenticator.Logout(r.Context(), a.sessions.SessionIDFromRequest(r))
	if err != nil {
		logbuilder.NewDefaultInfoLevelLogger().
			Error("[Logout]: failed", logbuilder.Fields{"error": err.Error()})
		jsmHttp.WriteJSONError(w, metrics.ErrInternalServerError, http.StatusInternalServerError)
		return
	}

	// Cleared session cookie plus a CSRF cookie rebound to the anonymous state.
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	jsmHttp.WriteJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}

func (a *auth) Authenticate(w http.ResponseWriter, r *http.Request) {
	logLevel := "INFO"
	disableColor := false
	authLogger, err := logbuilder.NewJsmLogger(logbuilder.Config{Level: &logLevel, DisableColors: &disableColor})
	defer func() {
		authLogger.Track("Handler.Authenticate", logbuilder.Fields{})
	}()

	if err != nil {
		jsmHttp.WriteJSONError(w, metrics.ErrInternalServerError, http.StatusInternalServerError)
		authLogger.Error("[Authenticate]: Internal Server Error", logbuilder.Fields{
			"time":  time.Now(),
			"error": err,
		})
		return
	}

	ctx := r.Context()
	jsmHttp.LimitBody(w, r)

	var logInRequest domain.LogInRequest
	if err := jsmHttp.Decode(ctx, r.Body, &logInRequest); err != nil {
		// An oversized body is a distinct condition from malformed JSON, and
		// 413 tells the client that retrying the same payload is pointless.
		status, code := http.StatusBadRequest, metrics.ErrBadRequest
		var maxBytes *http.MaxBytesError
		if errors.Is(err, jsmHttp.ErrBodyTooLarge) || errors.As(err, &maxBytes) {
			status, code = http.StatusRequestEntityTooLarge, metrics.ErrRequestTooLarge
		}
		jsmHttp.WriteJSONError(w, code, status)
		authLogger.Error("[Authenticate]: Error decoding body]", logbuilder.Fields{
			"error": err,
		})
		return
	}

	authCookies, err := a.authenticator.Authenticate(ctx, logInRequest.Email, logInRequest.Password)
	if err != nil {
		authLogger.Error("[Authenticate]: authentication failed", logbuilder.Fields{
			"error": err,
			"email": logInRequest.Email,
		})
		if errors.Is(err, service.ErrIncorrectCredentials) {
			jsmHttp.WriteJSONError(w, metrics.ErrIncorrectCredentials, http.StatusUnauthorized)
			return
		}
		jsmHttp.WriteJSONError(w, metrics.ErrInternalServerError, http.StatusInternalServerError)
		return
	}

	// Session cookie plus the CSRF cookie rebound to the new session — both
	// must land before the body, since WriteJSON commits the header.
	for _, c := range authCookies {
		http.SetCookie(w, c)
	}
	jsmHttp.WriteJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}
