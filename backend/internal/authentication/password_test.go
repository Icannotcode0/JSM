package authentication

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-battery") {
		t.Error("the correct password did not verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("a wrong password verified")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("identical passwords produced identical hashes; the salt is missing")
	}
	// Both must still verify — the salt travels inside the hash.
	if !VerifyPassword(a, "same-password") || !VerifyPassword(b, "same-password") {
		t.Error("a salted hash failed to verify")
	}
}

func TestVerifyRejectsGarbageHash(t *testing.T) {
	if VerifyPassword("not-a-bcrypt-hash", "anything") {
		t.Error("a malformed hash verified; VerifyPassword must fail closed")
	}
}

// bcrypt silently truncates at 72 bytes. This test documents that limit rather
// than asserting it is absent: the service is what has to reject long inputs,
// and this pins the reason why.
func TestBcryptTruncatesAtSeventyTwoBytes(t *testing.T) {
	base := strings.Repeat("a", 72)
	hash, err := HashPassword(base)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, base+"IGNORED-TAIL") {
		t.Skip("this bcrypt build does not truncate; the service cap is belt-and-braces")
	}
	t.Log("confirmed: bytes past 72 are ignored, which is why enforcePasswordComplexity caps in bytes")
}

// The whole point of BurnPasswordComparison: without it, "no such user" returns
// in ~2ms while "wrong password" takes ~60ms, and that gap enumerates accounts.
func TestBurnPasswordComparisonCostsRoughlyAVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	hash, err := HashPassword("a-real-password")
	if err != nil {
		t.Fatal(err)
	}

	measure := func(fn func()) time.Duration {
		start := time.Now()
		for i := 0; i < 3; i++ {
			fn()
		}
		return time.Since(start) / 3
	}

	real := measure(func() { VerifyPassword(hash, "wrong-guess") })
	burn := measure(func() { BurnPasswordComparison("wrong-guess") })

	// Generous bounds: this asserts "same order of magnitude", not a precise
	// figure, so it doesn't turn into a flaky test on a loaded machine.
	ratio := float64(burn) / float64(real)
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("burn=%v real=%v (ratio %.2f) — the timing oracle is open again", burn, real, ratio)
	}
}
