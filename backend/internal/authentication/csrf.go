package authentication

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// Signed double-submit CSRF tokens.
//
// A plain double-submit cookie ("set a random cookie, require the same value in
// a header") assumes an attacker cannot write cookies into the victim's
// browser. That assumption is weak for a localhost service, because **cookies
// are not port-scoped**: any page served from any other localhost origin — a
// second dev server, a downloaded HTML file — shares the cookie jar for host
// "localhost" and can therefore choose *both* halves of the pair. SameSite
// doesn't help either, since "site" ignores the port, so localhost:3000 and
// localhost:8080 are same-site.
//
// Signing closes that hole. A token is
//
//	<nonce> "!" <sessionID> "." <HMAC-SHA256(secret, nonce!sessionID)>
//
// so a token is only valid if this server minted it (the attacker has no
// secret) AND it is bound to the session presenting it. Binding also gives
// rotation for free: logging in changes the session ID, which retroactively
// invalidates every token minted before the privilege change.
//
// The session ID is empty for tokens minted before login — that's the
// anonymous token POST /login itself is authorised by, and it stops working the
// moment a session exists.

const (
	// Neither separator appears in base64url output or in a session ID, so the
	// token can always be split back apart unambiguously.
	csrfSigSeparator     = "."
	csrfBindingSeparator = "!"

	csrfNonceBytes = 32
)

// signCSRF returns the base64url HMAC of message under the manager's secret.
func (s *SessionManager) signCSRF(message string) string {
	mac := hmac.New(sha256.New, s.csrfSecret)
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// MintCSRFToken issues a fresh token bound to sessionID (empty for anonymous).
func (s *SessionManager) MintCSRFToken(sessionID string) (string, error) {
	nonce := make([]byte, csrfNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	message := base64.RawURLEncoding.EncodeToString(nonce) + csrfBindingSeparator + sessionID
	return message + csrfSigSeparator + s.signCSRF(message), nil
}

// VerifyCSRFToken reports whether token was minted by this server and is bound
// to sessionID. Both comparisons are constant-time.
func (s *SessionManager) VerifyCSRFToken(token, sessionID string) bool {
	sigAt := strings.LastIndex(token, csrfSigSeparator)
	if sigAt < 0 {
		return false
	}
	message, sig := token[:sigAt], token[sigAt+1:]

	// hmac.Equal is constant-time, and rejects any token this server didn't
	// sign — including one an attacker wrote into the cookie jar directly.
	if !hmac.Equal([]byte(sig), []byte(s.signCSRF(message))) {
		return false
	}

	bindAt := strings.LastIndex(message, csrfBindingSeparator)
	if bindAt < 0 {
		return false
	}
	boundSession := message[bindAt+1:]

	return subtle.ConstantTimeCompare([]byte(boundSession), []byte(sessionID)) == 1
}

// SessionIDFromRequest returns the raw session cookie value, or "" if absent.
//
// This is binding material only — it is deliberately *not* checked against
// Redis. A CSRF token should stay tied to the cookie the browser is actually
// presenting; whether that session is still live is SessionRequired's call.
func (s *SessionManager) SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(s.SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// NewCSRFCookie builds the cookie carrying a token bound to sessionID. Callers
// that change a session's identity (login, and later logout) must set this
// alongside the new session cookie, or the client keeps a token bound to the
// session it just left.
func (s *SessionManager) NewCSRFCookie(sessionID string) (*http.Cookie, error) {
	token, err := s.MintCSRFToken(sessionID)
	if err != nil {
		return nil, err
	}
	return s.csrfCookie(token), nil
}

func (s *SessionManager) csrfCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:  s.CsrfCookieName,
		Value: token,
		// Readable by JS on purpose: the client has to echo it back in the
		// X-CSRF-TOKEN header. That is what makes double-submit work, and it is
		// safe because a page on another origin still cannot read it.
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		Domain:   s.cookie.Domain,
	}
}
