package rate_limiter

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustPrefixes(t *testing.T, raw string) []netip.Prefix {
	t.Helper()
	p, err := ParseTrustedProxies(raw)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", raw, err)
	}
	return p
}

func requestFrom(peer string, headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = peer
	for name, values := range headers {
		for _, v := range values {
			r.Header.Add(name, v)
		}
	}
	return r
}

func TestClientIPTrustChain(t *testing.T) {
	cases := []struct {
		name    string
		trusted string
		peer    string
		headers map[string][]string
		want    string
	}{
		{
			name: "direct connection, no headers",
			peer: "203.0.113.7:44321",
			want: "203.0.113.7",
		},
		{
			// The whole reason trust is configuration. An untrusted peer's
			// headers are a claim, and believing them hands an attacker a fresh
			// bucket per request.
			name:    "direct connection ignores a spoofed header",
			peer:    "203.0.113.7:44321",
			headers: map[string][]string{"X-Forwarded-For": {"9.9.9.9"}},
			want:    "203.0.113.7",
		},
		{
			name:    "one trusted proxy",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"198.51.100.23"}},
			want:    "198.51.100.23",
		},
		{
			// The spoof sits left of the entry our proxy appended, so walking
			// from the right never reaches it.
			name:    "spoofed entry stays left of the real client",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"9.9.9.9, 198.51.100.23"}},
			want:    "198.51.100.23",
		},
		{
			name:    "two trusted proxies",
			trusted: "10.0.0.0/8,172.16.0.0/12",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"9.9.9.9, 198.51.100.23, 172.16.4.4"}},
			want:    "198.51.100.23",
		},
		{
			// Go does not join repeated headers. A parser reading only the
			// first would stop at the wrong hop.
			name:    "chain split across repeated headers",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"9.9.9.9", "198.51.100.23"}},
			want:    "198.51.100.23",
		},
		{
			name:    "every hop trusted falls back to the peer",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"10.0.0.9, 10.0.0.8"}},
			want:    "10.0.0.5",
		},
		{
			name:    "RFC 7239 Forwarded",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"Forwarded": {`for=198.51.100.23;proto=https`}},
			want:    "198.51.100.23",
		},
		{
			// Quoting is why splitForwarded cannot be a plain strings.Split.
			name:    "Forwarded with a quoted bracketed IPv6 and port",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"Forwarded": {`for="[2001:db8::1]:8080"`}},
			want:    "2001:db8::1",
		},
		{
			name:    "Forwarded chain walks right to left",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"Forwarded": {`for=9.9.9.9, for=198.51.100.23`}},
			want:    "198.51.100.23",
		},
		{
			// "unknown" and obfuscated identifiers are legal and carry no
			// address; they must not become a bucket of their own.
			name:    "unknown and obfuscated identifiers are skipped",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"Forwarded": {`for=unknown, for=_hidden, for=198.51.100.23`}},
			want:    "198.51.100.23",
		},
		{
			name:    "garbage entries are dropped, not trusted",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"not-an-ip, 198.51.100.23"}},
			want:    "198.51.100.23",
		},
		{
			name:    "client address carrying a port",
			trusted: "10.0.0.0/8",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"198.51.100.23:51234"}},
			want:    "198.51.100.23",
		},
		{
			name:    "single trusted host, not a CIDR",
			trusted: "10.0.0.5",
			peer:    "10.0.0.5:8080",
			headers: map[string][]string{"X-Forwarded-For": {"198.51.100.23"}},
			want:    "198.51.100.23",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewClientIPResolver(ProxyConfig{TrustedProxies: mustPrefixes(t, tc.trusted)})
			got := r.ClientIP(requestFrom(tc.peer, tc.headers))
			if got.String() != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// A platform header is unambiguous, but only once the peer is trusted — anyone
// can set CF-Connecting-IP, so it means nothing on its own.
func TestPlatformHeader(t *testing.T) {
	cfg := ProxyConfig{
		TrustedProxies: mustPrefixes(t, "10.0.0.0/8"),
		ClientIPHeader: "CF-Connecting-IP",
	}

	t.Run("honoured behind a trusted proxy", func(t *testing.T) {
		r := NewClientIPResolver(cfg)
		got := r.ClientIP(requestFrom("10.0.0.5:8080", map[string][]string{
			"CF-Connecting-IP": {"198.51.100.23"},
			"X-Forwarded-For":  {"9.9.9.9"},
		}))
		if got.String() != "198.51.100.23" {
			t.Errorf("got %s, want the platform header to win", got)
		}
	})

	t.Run("ignored on a direct connection", func(t *testing.T) {
		r := NewClientIPResolver(cfg)
		got := r.ClientIP(requestFrom("203.0.113.7:44321", map[string][]string{
			"CF-Connecting-IP": {"9.9.9.9"},
		}))
		if got.String() != "203.0.113.7" {
			t.Errorf("got %s — an untrusted peer's platform header was believed", got)
		}
	})
}

// IPv6 is bucketed by /64. A residential customer routinely holds a /64 or
// larger, so limiting a /128 would give one attacker 2^64 free buckets.
func TestBucketValueGroupsIPv6(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":           "203.0.113.7",
		"::ffff:203.0.113.7":    "203.0.113.7",
		"2001:db8:1:2::1":       "2001:db8:1:2::/64",
		"2001:db8:1:2:dead::99": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":       "2001:db8:1:3::/64",
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := bucketValue(netip.MustParseAddr(in)); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

// Two addresses in one allocation must share a bucket; two allocations must not.
func TestBucketValueSeparatesDistinctAllocations(t *testing.T) {
	same := bucketValue(netip.MustParseAddr("2001:db8:1:2::1")) ==
		bucketValue(netip.MustParseAddr("2001:db8:1:2:ffff::ffff"))
	if !same {
		t.Error("addresses in one /64 landed in different buckets")
	}

	different := bucketValue(netip.MustParseAddr("2001:db8:1:2::1")) !=
		bucketValue(netip.MustParseAddr("2001:db8:9:9::1"))
	if !different {
		t.Error("distinct /64s were bucketed together")
	}
}

// IPv4 reaching the server over IPv6 must not get a second bucket.
func TestBucketValueUnmapsIPv4(t *testing.T) {
	plain := bucketValue(netip.MustParseAddr("203.0.113.7"))
	mapped := bucketValue(netip.MustParseAddr("::ffff:203.0.113.7"))
	if plain != mapped {
		t.Errorf("%s and %s are the same client", plain, mapped)
	}
}

func TestSplitForwardedRejectsUnterminatedQuote(t *testing.T) {
	if _, err := splitForwarded(`for="[2001:db8::1]:8080`); err == nil {
		t.Error("an unterminated quoted string parsed cleanly")
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	if _, err := ParseTrustedProxies("10.0.0.0/8, nonsense"); err == nil {
		t.Error("a malformed CIDR was accepted")
	}
}
