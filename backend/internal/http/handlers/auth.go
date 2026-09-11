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
		jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
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
		jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
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
		status, code := http.StatusBadRequest, metrics.CodeBadRequest
		var maxBytes *http.MaxBytesError
		if errors.Is(err, jsmHttp.ErrBodyTooLarge) || errors.As(err, &maxBytes) {
			status, code = http.StatusRequestEntityTooLarge, metrics.CodeRequestTooLarge
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
		if errors.Is(err, metrics.ErrIncorrectCredentials) {
			jsmHttp.WriteJSONError(w, metrics.CodeIncorrectCredentials, http.StatusUnauthorized)
			return
		}
		jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
		return
	}

	// Session cookie plus the CSRF cookie rebound to the new session — both
	// must land before the body, since WriteJSON commits the header.
	for _, c := range authCookies {
		http.SetCookie(w, c)
	}
	jsmHttp.WriteJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}

func (a *auth) ResetPassword(w http.ResponseWriter, r *http.Request) {
	resetLogger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		resetLogger.Track("Handler.ResetPassword", logbuilder.Fields{})
	}()

	// requireUser already writes 401 when the context has no user. Writing
	// again here would commit a second status and body on the same response.
	userId, ok := requireUser(w, r)
	if !ok {
		return
	}

	jsmHttp.LimitBody(w, r)

	req := &domain.ChangePasswordRequest{}
	if err := jsmHttp.Decode(r.Context(), r.Body, req); err != nil {
		status, code := http.StatusBadRequest, metrics.CodeBadRequest
		var maxBytes *http.MaxBytesError
		if errors.Is(err, jsmHttp.ErrBodyTooLarge) || errors.As(err, &maxBytes) {
			status, code = http.StatusRequestEntityTooLarge, metrics.CodeRequestTooLarge
		}
		jsmHttp.WriteJSONError(w, code, status)
		return
	}

	// The session to end is the one presenting this request; the service can't
	// read it off the cookie, so it is resolved here and passed down.
	cookies, err := a.authenticator.ChangePassword(
		r.Context(), userId, a.sessions.SessionIDFromRequest(r), *req,
	)
	if err != nil {
		writeServiceError(w, "ResetPassword", err)
		return
	}

	// Cleared session cookie plus a CSRF cookie rebound to the anonymous state.
	// Both must land before the body, since WriteJSON commits the header.
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	jsmHttp.WriteJSON(w, map[string]string{"status": "ok"}, http.StatusOK)
}

// SignUp handler takes a sign-up request from the frontend, parses the email, name and the password,
// calls service to store them properly. Login DOES NOT give the session Token, frontend will redirect
// the user to the login page, the user will use their credentials registered to authenticate themselves

// Service Layer should perform the verifications of the email, name and password

// TODO: Will integrate Oauth 2.0

func (a *auth) SignUp(w http.ResponseWriter, r *http.Request) {
	logger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		logger.Track("Handler.SignUp", logbuilder.Fields{})
	}()

	ctx := r.Context()
	signUpRequest := &domain.SignUpRequest{}
	jsmHttp.LimitBody(w, r)
	if err := jsmHttp.Decode(r.Context(), r.Body, signUpRequest); err != nil {
		status, code := http.StatusBadRequest, metrics.CodeBadRequest
		var maxBytes *http.MaxBytesError
		if errors.Is(err, jsmHttp.ErrBodyTooLarge) || errors.As(err, &maxBytes) {
			status, code = http.StatusRequestEntityTooLarge, metrics.CodeRequestTooLarge
		}
		jsmHttp.WriteJSONError(w, code, status)
		return
	}

	user, err := a.authenticator.CreateUser(ctx, *signUpRequest)
	if err != nil {
		// errors.Is against a shared sentinel, not strings.Contains on the
		// message. Matching on text breaks the moment someone rewords an error,
		// and it silently matches the wrong condition when one message happens
		// to contain another.
		var invalid metrics.InvalidInputError
		switch {
		case errors.Is(err, metrics.ErrEmailTaken):
			// 409, not 401: the request was authentic and well-formed, the
			// address is simply taken.
			jsmHttp.WriteJSONError(w, metrics.CodeEmailAlreadyTaken, http.StatusConflict)

		case errors.As(err, &invalid):
			jsmHttp.WriteJSONError(w, invalid.Reason, http.StatusBadRequest)
		default:
			logger.Error("[SignUp]: failed", logbuilder.Fields{"error": err.Error()})
			jsmHttp.WriteJSONError(w, metrics.CodeInternalServerError, http.StatusInternalServerError)
		}
		return
	}

	// 201 with the created user, per API.md. domain.User tags PasswordHash
	// `json:"-"`, so the hash cannot leak through this response.
	jsmHttp.WriteJSON(w, map[string]any{"user": user}, http.StatusCreated)
}
