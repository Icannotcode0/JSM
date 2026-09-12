package rate_limiter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testPolicy(rules ...Rule) Policy {
	return Policy{policyKey(ScopeLogin, KeyIP): rules}
}

func ipKey(value string) Key {
	return Key{Scope: ScopeLogin, Type: KeyIP, Value: value}
}

// The most important behaviour in the package: a limiter that never denies is
// indistinguishable from no limiter at all, and nothing else here matters if
// this is wrong.
func TestAllowDeniesPastTheLimit(t *testing.T) {
	l := NewMemoryLimiter(WithPolicy(testPolicy(Rule{Limit: 3, Window: time.Minute})))
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		d, err := l.Allow(ctx, ipKey("1.2.3.4"))
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed {
			t.Fatalf("request %d denied while still inside the limit", i)
		}
	}

	d, err := l.Allow(ctx, ipKey("1.2.3.4"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("the 4th request was allowed under a limit of 3")
	}
	if d.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want a positive duration", d.RetryAfter)
	}
}

func TestBucketsAreIndependentPerValue(t *testing.T) {
	l := NewMemoryLimiter(WithPolicy(testPolicy(Rule{Limit: 1, Window: time.Minute})))
	ctx := context.Background()

	if _, err := l.Allow(ctx, ipKey("1.2.3.4")); err != nil {
		t.Fatal(err)
	}
	d, err := l.Allow(ctx, ipKey("5.6.7.8"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Error("one client's usage counted against another's bucket")
	}
}

// A single window is trivially paced around: exhaust it, wait for the boundary,
// repeat. The second tier is what stops that, and this is the test that proves
// the burst rule refilling does not release the sustained one.
func TestSustainedRuleSurvivesTheBurstWindow(t *testing.T) {
	now := time.Now().Truncate(time.Hour)
	clock := func() time.Time { return now }

	l := NewMemoryLimiter(
		WithPolicy(testPolicy(
			Rule{Limit: 2, Window: time.Minute},
			Rule{Limit: 3, Window: time.Hour},
		)),
		WithClock(func() time.Time { return clock() }),
	)
	ctx := context.Background()

	// Burn the burst rule, then step past its window.
	for i := 0; i < 2; i++ {
		if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
			t.Fatalf("request %d denied inside both limits", i+1)
		}
	}
	now = now.Add(2 * time.Minute)

	// The burst bucket has refilled, so this passes — and consumes the third
	// and final unit of the hourly rule.
	if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
		t.Fatal("denied after the burst window refilled")
	}

	now = now.Add(2 * time.Minute)
	d, _ := l.Allow(ctx, ipKey("1.2.3.4"))
	if d.Allowed {
		t.Error("waiting out the burst window bypassed the sustained rule")
	}
}

func TestWindowRefills(t *testing.T) {
	now := time.Now().Truncate(time.Hour)
	l := NewMemoryLimiter(
		WithPolicy(testPolicy(Rule{Limit: 1, Window: time.Minute})),
		WithClock(func() time.Time { return now }),
	)
	ctx := context.Background()

	if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
		t.Fatal("first request denied")
	}
	if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); d.Allowed {
		t.Fatal("second request allowed under a limit of 1")
	}

	now = now.Add(time.Minute)
	if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
		t.Error("still denied after the window elapsed")
	}
}

// Login calls Reset on success so someone who mistyped twice and then got it
// right is not left one attempt from a lockout.
func TestResetClearsTheCounter(t *testing.T) {
	l := NewMemoryLimiter(WithPolicy(testPolicy(Rule{Limit: 2, Window: time.Minute})))
	ctx := context.Background()

	l.Allow(ctx, ipKey("1.2.3.4"))
	l.Allow(ctx, ipKey("1.2.3.4"))

	if err := l.Reset(ctx, ipKey("1.2.3.4")); err != nil {
		t.Fatal(err)
	}
	if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
		t.Error("Reset did not clear the bucket")
	}
}

// Denying on the first exhausted key and skipping the rest would let an
// attacker keep one bucket artificially cool by deliberately tripping another.
func TestEveryKeyIsConsumedEvenAfterADenial(t *testing.T) {
	policy := Policy{
		policyKey(ScopeLogin, KeyIP):    {Rule{Limit: 1, Window: time.Minute}},
		policyKey(ScopeLogin, KeyEmail): {Rule{Limit: 5, Window: time.Minute}},
	}
	l := NewMemoryLimiter(WithPolicy(policy))
	ctx := context.Background()

	email := Key{Scope: ScopeLogin, Type: KeyEmail, Value: "ada@example.com"}

	// Exhaust the IP rule, then send four more requests that are all denied on
	// IP — but must still consume the email bucket.
	for i := 0; i < 5; i++ {
		l.Allow(ctx, ipKey("1.2.3.4"), email)
	}

	// The email rule has now seen 5 of its 5. A request from a *different*
	// address must therefore be denied on email alone.
	d, err := l.Allow(ctx, ipKey("9.9.9.9"), email)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Error("the email bucket was not consumed while the IP rule was denying")
	}
}

// A scope with no policy entry is how an endpoint opts out — a missing rule is
// configuration, not an error.
func TestUnconfiguredScopeIsUnlimited(t *testing.T) {
	l := NewMemoryLimiter(WithPolicy(Policy{}))
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		if d, _ := l.Allow(ctx, ipKey("1.2.3.4")); !d.Allowed {
			t.Fatalf("denied at request %d under an empty policy", i+1)
		}
	}
}

/* ---------- fail-closed ------------------------------------------------- */

type failingStore struct{ err error }

func (f failingStore) Incr(context.Context, string, time.Duration) (int, time.Duration, error) {
	return 0, 0, f.err
}
func (f failingStore) Del(context.Context, ...string) error { return f.err }

// Failing open would hand unlimited signups to anyone who can degrade the store
// — and nothing would reveal that limiting had stopped.
func TestStoreFailureDenies(t *testing.T) {
	boom := errors.New("redis is down")
	l := newLimiter(failingStore{err: boom}, WithPolicy(testPolicy(Rule{Limit: 10, Window: time.Minute})))

	d, err := l.Allow(context.Background(), ipKey("1.2.3.4"))
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the store error", err)
	}
	if d.Allowed {
		t.Error("a store failure allowed the request through")
	}
}

/* ---------- key construction -------------------------------------------- */

// Keys carry email addresses and IPs. A limiter should not turn the session
// store into a list of every address anyone has typed.
func TestBucketKeyDoesNotContainTheRawValue(t *testing.T) {
	l := newLimiter(nil)
	key := l.bucket(Key{Scope: ScopeLogin, Type: KeyEmail, Value: "ada@example.com"}, Rule{Limit: 1, Window: time.Minute})

	if contains(key, "ada@example.com") || contains(key, "ada") {
		t.Errorf("bucket key leaks the raw value: %s", key)
	}
}

func TestBucketKeySeparatesRulesAndScopes(t *testing.T) {
	l := newLimiter(nil)
	value := Key{Scope: ScopeLogin, Type: KeyIP, Value: "1.2.3.4"}

	burst := l.bucket(value, Rule{Limit: 1, Window: time.Minute})
	sustained := l.bucket(value, Rule{Limit: 1, Window: time.Hour})
	if burst == sustained {
		t.Error("two rules on one key share a bucket")
	}

	other := l.bucket(Key{Scope: ScopeSignup, Type: KeyIP, Value: "1.2.3.4"}, Rule{Limit: 1, Window: time.Minute})
	if burst == other {
		t.Error("two scopes share a bucket")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
