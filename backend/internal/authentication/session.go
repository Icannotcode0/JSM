package authentication

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Icannotcode0/job-app-manager/backend/internal/store/redis"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// domain of the cookie and if it is HTTPS only
type CookieSettings struct {
	Domain string
	Secure bool
}

// SessionManager owns session *policy* (cookie names, TTLs, security flags)
// and delegates all actual Redis reads/writes to a SessionStore — this
// package never issues a raw Redis command itself.
type SessionManager struct {
	Store              *cachestore.SessionStore
	TTL                time.Duration
	SessionCookieName  string
	CsrfCookieName     string
	RedirectCookieName string
	RedirectTTL        time.Duration
	cookie             CookieSettings
	// csrfSecret signs CSRF tokens (see csrf.go). Unexported: anything that can
	// read it can mint tokens.
	csrfSecret []byte
}

func NewSessionManager(
	store *cachestore.SessionStore,
	ttl time.Duration,
	sessionCookieName string,
	csrfCookieName string,
	redirectCookieName string,
	redirectTTL time.Duration,
	cookie CookieSettings,
	csrfSecret []byte,
) *SessionManager {
	return &SessionManager{
		Store:              store,
		TTL:                ttl,
		SessionCookieName:  sessionCookieName,
		CsrfCookieName:     csrfCookieName,
		RedirectCookieName: redirectCookieName,
		RedirectTTL:        redirectTTL,
		cookie:             cookie,
		csrfSecret:         csrfSecret,
	}
}

func generateNewToken() (string, error) {

	newToken := uuid.New()
	// URLEncoding ensures the resulting string is safe for use in URLs and cookies.
	return base64.URLEncoding.EncodeToString(newToken[:]), nil
}

// CreateSession creates a new Redis-backed session for userID and returns the
// session ID plus the cookie the caller should set on the response. It does
// not touch gin.Context or write anything itself — that's the handler's job —
// so this stays testable without an HTTP request in play.
func (s *SessionManager) CreateSession(ctx context.Context, userID, email, username string) (sid string, cookie *http.Cookie, err error) {
	sid, err = generateNewToken()
	if err != nil {
		log.Printf("[CreateSession] Session ID Generation Failure")
		return "", nil, err
	}

	fields := map[string]any{
		"user_id":          userID,
		"email":            email,
		"user_name":        username,
		"lastLoginAttempt": time.Now(),
	}
	if err := s.Store.Create(ctx, sid, fields, s.TTL); err != nil {
		log.Printf("[CreateSession] Failed to write session to store: %v", err)
		return "", nil, err
	}

	cookie = &http.Cookie{
		Name:     s.SessionCookieName,
		Value:    sid,
		MaxAge:   int(s.TTL.Seconds()),
		Domain:   s.cookie.Domain,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		Secure:   s.cookie.Secure,
	}
	return sid, cookie, nil
}

// DeleteSession deletes the Redis session entry for sid and returns the
// cookie the caller should set to clear the client's session cookie.
func (s *SessionManager) DeleteSession(ctx context.Context, sid string) (*http.Cookie, error) {
	if err := s.Store.Delete(ctx, sid); err != nil {
		log.Printf("[DeleteSession]: Failed to delete session from store: %v", err)
		return nil, err
	}
	return s.ClearSessionCookie(), nil
}

// ClearSessionCookie returns the cookie that removes the client's session.
//
// MaxAge -1 tells the browser to delete it outright. Every other attribute has
// to match the cookie set by CreateSession — a browser treats a cookie with a
// different Path or Domain as a *different* cookie and would leave the original
// in place while adding an empty one beside it.
func (s *SessionManager) ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     s.SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   s.cookie.Domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// GetUserIDFromRequest resolves userID from a session ID via Redis. Returns
// an empty string (no error) if the session doesn't exist or has expired.
func (s *SessionManager) GetUserIDFromRequest(ctx context.Context, sessID string) (string, error) {
	sess, err := s.Store.Get(ctx, sessID)
	if err != nil {
		log.Printf("[GetUserIDFromRequest]: Failed to query session from store: %v", err)
		return "", err
	}
	return sess["user_id"], nil
}

// EnsureCSRFToken returns a CSRF token valid for this request's session,
// issuing a fresh one when the request has none or presents one that no longer
// applies.
//
// "No longer applies" is the rotation mechanism: a token carries the session it
// was minted for, so the one obtained before login stops verifying the moment a
// session cookie exists, and the next safe request mints a replacement bound to
// it. Reusing a pre-login token afterwards is exactly the fixation this
// prevents.
func (s *SessionManager) EnsureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	sessionID := s.SessionIDFromRequest(r)

	if existing, err := r.Cookie(s.CsrfCookieName); err == nil && existing.Value != "" {
		if s.VerifyCSRFToken(existing.Value, sessionID) {
			return existing.Value, nil
		}
	}

	cookie, err := s.NewCSRFCookie(sessionID)
	if err != nil {
		// Previously this logged and carried on, setting an empty cookie and
		// reporting success. Fail closed instead — the caller turns this into a
		// 500 rather than handing the client a token that can never validate.
		log.Printf("[EnsureCSRFToken]: failed to mint CSRF token: %v", err)
		return "", err
	}

	http.SetCookie(w, cookie)
	return cookie.Value, nil
}

// SetRedirectCookie stores a safe return path for post-login redirect.
func (s *SessionManager) SetRedirectCookie(c *gin.Context, returnTo string) {
	returnTo = sanitizeReturnTo(returnTo)
	if returnTo == "" {
		return
	}

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.RedirectCookieName,
		Value:    returnTo,
		Path:     "/",
		Domain:   s.cookie.Domain,
		MaxAge:   int(s.RedirectTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *SessionManager) PopRedirectCookie(c *gin.Context, fallback string) string {
	val := ""
	redirectURL, err := c.Cookie(s.RedirectCookieName)
	if err != nil {
		val = sanitizeReturnTo(redirectURL)
	}

	// Clear redirect cookie
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.RedirectCookieName,
		Value:    "",
		Path:     "/",
		Domain:   s.cookie.Domain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookie.Secure,
		SameSite: http.SameSiteLaxMode,
	})

	if val == "" {
		return fallback
	}
	return val
}

func sanitizeReturnTo(p string) string {
	// allow only relative paths like "/dashboard"
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	// block protocol-relative URLs like "//evil.com"
	if strings.HasPrefix(p, "//") {
		return ""
	}
	return p
}
