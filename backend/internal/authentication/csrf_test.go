package authentication

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestManager builds a SessionManager with only the fields the CSRF code
// touches. Store is nil on purpose: none of these paths reach Redis, so a nil
// store proves that rather than hiding it behind a fake.
func newTestManager(secret string) *SessionManager {
	return &SessionManager{
		SessionCookieName: "jsm_session",
		CsrfCookieName:    "jsm_csrf",
		csrfSecret:        []byte(secret),
	}
}

func TestMintAndVerifyRoundTrip(t *testing.T) {
	sm := newTestManager("test-secret")

	for _, session := range []string{"", "sid-abc123"} {
		token, err := sm.MintCSRFToken(session)
		if err != nil {
			t.Fatalf("MintCSRFToken(%q): %v", session, err)
		}
		if !sm.VerifyCSRFToken(token, session) {
			t.Errorf("token minted for %q did not verify against it", session)
		}
	}
}

func TestTokensAreUnique(t *testing.T) {
	sm := newTestManager("test-secret")

	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		token, err := sm.MintCSRFToken("sid")
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatal("MintCSRFToken returned a duplicate; the nonce is not random")
		}
		seen[token] = true
	}
}

// The reason the token is signed at all: cookies are not port-scoped, so a page
// on any other localhost origin can write jsm_csrf and choose both halves of
// the double-submit pair. Only the HMAC stops that.
func TestForgedTokensRejected(t *testing.T) {
	sm := newTestManager("test-secret")

	genuine, err := sm.MintCSRFToken("sid")
	if err != nil {
		t.Fatal(err)
	}

	tampered := genuine[:len(genuine)-1] + "A"
	if tampered == genuine {
		tampered = genuine[:len(genuine)-1] + "B"
	}

	cases := map[string]string{
		"attacker-chosen value":    "whatever-i-want",
		"well-formed but unsigned": "AAAA!sid.BBBB",
		"no separators":            "nonsense",
		"empty":                    "",
		"signature byte flipped":   tampered,
	}

	for name, token := range cases {
		if sm.VerifyCSRFToken(token, "sid") {
			t.Errorf("%s: forged token accepted", name)
		}
	}
}

// A token signed by a different server must not verify here — this is what
// makes SESSION_SECRET the thing that has to stay secret.
func TestTokenFromAnotherSecretRejected(t *testing.T) {
	mine := newTestManager("my-secret")
	theirs := newTestManager("their-secret")

	token, err := theirs.MintCSRFToken("sid")
	if err != nil {
		t.Fatal(err)
	}
	if mine.VerifyCSRFToken(token, "sid") {
		t.Error("token signed with a different secret was accepted")
	}
}

// Binding is what gives rotation for free: logging in changes the session ID,
// which retroactively invalidates every token minted before that.
func TestTokenIsBoundToItsSession(t *testing.T) {
	sm := newTestManager("test-secret")

	anonymous, err := sm.MintCSRFToken("")
	if err != nil {
		t.Fatal(err)
	}
	if !sm.VerifyCSRFToken(anonymous, "") {
		t.Fatal("anonymous token should verify with no session")
	}
	if sm.VerifyCSRFToken(anonymous, "sid-after-login") {
		t.Error("pre-login token still valid once a session exists")
	}

	bound, err := sm.MintCSRFToken("sid-one")
	if err != nil {
		t.Fatal(err)
	}
	if sm.VerifyCSRFToken(bound, "sid-two") {
		t.Error("token accepted against a different session")
	}
	if sm.VerifyCSRFToken(bound, "") {
		t.Error("session-bound token accepted as anonymous")
	}
}

func TestSessionIDFromRequest(t *testing.T) {
	sm := newTestManager("test-secret")

	t.Run("absent", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := sm.SessionIDFromRequest(r); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("present", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: "jsm_session", Value: "sid-abc"})
		if got := sm.SessionIDFromRequest(r); got != "sid-abc" {
			t.Errorf("got %q, want %q", got, "sid-abc")
		}
	})
}

func TestNewCSRFCookieIsReadableByJS(t *testing.T) {
	sm := newTestManager("test-secret")

	cookie, err := sm.NewCSRFCookie("sid")
	if err != nil {
		t.Fatal(err)
	}
	// Not HttpOnly on purpose — the client has to read it to echo it back in
	// the X-CSRF-TOKEN header. Flipping this silently breaks every mutation.
	if cookie.HttpOnly {
		t.Error("CSRF cookie is HttpOnly; the client can no longer read it")
	}
	if cookie.Name != "jsm_csrf" || cookie.Path != "/" {
		t.Errorf("unexpected cookie name/path: %q %q", cookie.Name, cookie.Path)
	}
	if !strings.Contains(cookie.Value, "!") || !strings.Contains(cookie.Value, ".") {
		t.Errorf("cookie value is not a signed+bound token: %q", cookie.Value)
	}
}

func TestEnsureCSRFTokenReissuesOnSessionChange(t *testing.T) {
	sm := newTestManager("test-secret")

	// Cold client: no cookie at all.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	first, err := sm.EnsureCSRFToken(w, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("no cookie set for a client that had none")
	}

	// Same session, existing valid token: reused, not reissued.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/health", nil)
	r2.AddCookie(&http.Cookie{Name: "jsm_csrf", Value: first})
	second, err := sm.EnsureCSRFToken(w2, r2)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Error("a still-valid token was needlessly reissued")
	}

	// A session now exists, so the anonymous token no longer applies.
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodGet, "/health", nil)
	r3.AddCookie(&http.Cookie{Name: "jsm_csrf", Value: first})
	r3.AddCookie(&http.Cookie{Name: "jsm_session", Value: "sid-new"})
	third, err := sm.EnsureCSRFToken(w3, r3)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Error("token was not rotated after the session changed")
	}
	if !sm.VerifyCSRFToken(third, "sid-new") {
		t.Error("reissued token is not bound to the new session")
	}
}
