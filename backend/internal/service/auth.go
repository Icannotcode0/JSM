package service

import (
	"context"
	"errors"
	"net/http"

	authen "github.com/Icannotcode0/job-app-manager/backend/internal/authentication"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/logbuilder"
	"github.com/Icannotcode0/job-app-manager/backend/internal/common/metrics"
	"github.com/Icannotcode0/job-app-manager/backend/internal/store"
)

// ErrIncorrectCredentials is returned for both "no such user" and "wrong
// password" — never let the caller distinguish which, so a failed login
// never reveals whether a given email is registered.
var ErrIncorrectCredentials = errors.New(metrics.ErrIncorrectCredentials)

type auth struct {
	store *store.Store
	sm    *authen.SessionManager
}

func NewAuth(store *store.Store, sm *authen.SessionManager) *auth {
	return &auth{
		store: store,
		sm:    sm,
	}
}

// Authenticate verifies email/password and, on success, creates a session,
// returning the cookies the handler should set — the minimal design: /me is the
// single source of truth for user info, not the login response.
//
// Two cookies come back, not one. The session cookie is the obvious half; the
// CSRF cookie has to be reissued in the same response because tokens are bound
// to a session (see authentication/csrf.go), so the one the client used to
// authorise this very request stops being valid the instant the session exists.
func (a *auth) Authenticate(ctx context.Context, email string, password string) ([]*http.Cookie, error) {

	authLogger := logbuilder.NewDefaultInfoLevelLogger()
	defer func() {
		authLogger.Track("Auth.Service")
	}()

	user, err := a.store.AuthenticateStore.LookupUserByEmail(ctx, email)
	if err != nil {
		// Spend the same time bcrypt would have, so "no such user" and "wrong
		// password" are indistinguishable by duration as well as by message.
		// Without this the two paths differ by ~30x, which enumerates accounts.
		authen.BurnPasswordComparison(password)
		authLogger.Warn("login failed: user lookup error", logbuilder.Fields{"email": email})
		return nil, ErrIncorrectCredentials
	}

	if !authen.VerifyPassword(user.PasswordHash, password) {
		authLogger.Warn("login failed: password mismatch", logbuilder.Fields{
			"email": email,
			"name":  user.Name,
		})
		return nil, ErrIncorrectCredentials
	}

	sid, sessionCookie, err := a.sm.CreateSession(ctx, user.ID.Hex(), user.Email, user.Name)
	if err != nil {
		return nil, err
	}

	csrfCookie, err := a.sm.NewCSRFCookie(sid)
	if err != nil {
		return nil, err
	}

	return []*http.Cookie{sessionCookie, csrfCookie}, nil
}

// Logout destroys the session identified by sessionID and returns the cookies
// that put the client back in a signed-out state.
//
// It is deliberately idempotent: an empty or unknown sessionID is not an error.
// API.md specifies that logging out without a session still answers 200, and
// that is the right behaviour — "make sure I am signed out" has succeeded when
// the client holds no session, however it got there. Reporting a failure would
// only invite a retry loop over something already true.
//
// Two cookies come back for the same reason login returns two: CSRF tokens are
// bound to a session, so ending one must hand over a token bound to the
// anonymous state, or the client's next login POST is rejected.
func (a *auth) Logout(ctx context.Context, sessionID string) ([]*http.Cookie, error) {
	authLogger := logbuilder.NewDefaultInfoLevelLogger()

	sessionCookie := a.sm.ClearSessionCookie()

	if sessionID != "" {
		if _, err := a.sm.DeleteSession(ctx, sessionID); err != nil {
			// Clear the client side anyway. A Redis failure here means sessions
			// can't be *validated* either — SessionRequired reads the same
			// store — so nobody is authenticated regardless, and the entry
			// expires on its own TTL. Leaving the user visibly signed in
			// because of a backend hiccup is the worse outcome.
			authLogger.Error("logout: failed to delete session", logbuilder.Fields{
				"error": err.Error(),
			})
		}
	}

	csrfCookie, err := a.sm.NewCSRFCookie("")
	if err != nil {
		return nil, err
	}

	return []*http.Cookie{sessionCookie, csrfCookie}, nil
}
