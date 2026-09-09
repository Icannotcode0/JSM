package authentication

import (
	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash of password, safe to store in the DB.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches the given bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is a real bcrypt hash of a fixed string, generated once at startup
// at exactly the cost real passwords use. It exists only to be compared
// against — see BurnPasswordComparison.
//
// Generated rather than hardcoded so it can never drift out of sync with
// bcrypt.DefaultCost: the entire point is that verifying against it costs the
// same as verifying a real hash.
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("jsm-constant-time-login-padding"), bcrypt.DefaultCost)
	if err != nil {
		// Only reachable on an invalid cost or a >72-byte password, neither of
		// which is possible with the fixed inputs above.
		panic("authentication: cannot build dummy bcrypt hash: " + err.Error())
	}
	return h
}()

// BurnPasswordComparison does the same bcrypt work VerifyPassword does, against
// a throwaway hash, and discards the result.
//
// Call it on the "no such user" path. Without it a login for an unknown email
// answers in ~2ms while a login for a known email with the wrong password takes
// ~60ms, because only the second one reaches bcrypt. That gap is a reliable
// oracle for "does this email have an account here?", which defeats the point
// of returning an identical error for both cases.
func BurnPasswordComparison(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

func mapResponse(target *string) {
	if *target == "" {
		*target = "1"
		return
	}
	elementMapper := [][]string{}
	for i := 0; i < len(*target); i++ {
		elementMapper = append(elementMapper, []string{string((*target)[i])})
	}
}
