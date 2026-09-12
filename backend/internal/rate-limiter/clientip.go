package rate_limiter

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolving the client address.
//
// Per RFC 7239, the standard Forwarded header lets proxies and load balancers
// disclose connection info about the hop they received:
//
//	Forwarded: for=192.0.2.60;proto=http;host=203.0.113.43
//
// Multiple records are comma-separated with the earliest on the far left, and
// parameters within a record are semicolon-separated. Only "for" matters here.
// X-Forwarded-For is the older, more widely emitted equivalent and carries the
// same ordering.
//
// The address a client claims is never trustworthy. Anyone can send
// "Forwarded: for=198.51.100.99", and an attacker who varies it per request
// gets a fresh bucket every time — which makes the limiter decorative. The only
// value that cannot be forged is the TCP peer.
//
// But ignoring the headers is equally broken: behind a load balancer every user
// shares the balancer's address, so the first burst locks out everyone. There
// is no default that is safe in both deployments, so trust is configuration.
//
// The resolution walks the chain from the right — the end our own proxy wrote —
// leftward, stopping at the first address not on the trust list. Spoofed
// entries sit on the far left and are never reached.

// ClientIPResolver turns a request into the address to limit on.
type ClientIPResolver interface {
	ClientIP(r *http.Request) netip.Addr

	// BucketValue is the string form used as a rate-limit key. It is not
	// simply the address — see bucketValue for why IPv6 is grouped.
	BucketValue(r *http.Request) string
}

// ProxyConfig describes what the deployment sits behind.
//
// The zero value trusts nothing, which is the safe default. The two
// misconfigurations fail very differently: forgetting to declare a proxy keys
// every user on the balancer and locks out the world within seconds, while
// trusting too broadly is a silent bypass discovered after the fact. Defaulting
// closed makes the mistake the loud one.
type ProxyConfig struct {
	// TrustedProxies are CIDRs whose forwarding headers are believed.
	TrustedProxies []netip.Prefix

	// ClientIPHeader names a single-value header the platform guarantees, such
	// as CF-Connecting-IP or Fly-Client-IP. It is still only read when the peer
	// is trusted: anyone can set that header, so it means nothing on its own.
	ClientIPHeader string
}

type resolver struct {
	trusted []netip.Prefix
	header  string
}

// NewClientIPResolver builds a resolver and logs the trust set, so a
// misconfiguration is visible in the startup output rather than discovered
// through a lockout.
func NewClientIPResolver(cfg ProxyConfig) ClientIPResolver {
	r := &resolver{trusted: cfg.TrustedProxies, header: http.CanonicalHeaderKey(cfg.ClientIPHeader)}

	if len(r.trusted) == 0 {
		log.Print("rate_limiter: no trusted proxies configured — using the TCP peer address")
	} else {
		names := make([]string, 0, len(r.trusted))
		for _, p := range r.trusted {
			names = append(names, p.String())
		}
		log.Printf("rate_limiter: trusting forwarding headers from %s", strings.Join(names, ", "))
	}
	return r
}

func (r *resolver) isTrusted(addr netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (r *resolver) ClientIP(req *http.Request) netip.Addr {
	peer := peerAddr(req)

	// A direct connection: nothing the client sent is credible.
	if !peer.IsValid() || !r.isTrusted(peer) {
		return peer
	}

	// A platform header, where configured, is unambiguous — no chain to walk.
	// Reached only because the peer is trusted.
	if r.header != "" {
		if raw := req.Header.Get(r.header); raw != "" {
			if addr, err := parseAddr(raw); err == nil {
				return addr
			}
		}
	}

	// Walk right to left. The rightmost entry was written by our own closest
	// proxy and is the most trustworthy; trust decreases leftward.
	chain := forwardedChain(req)
	for i := len(chain) - 1; i >= 0; i-- {
		if !r.isTrusted(chain[i]) {
			return chain[i]
		}
	}

	// Every hop was trusted — nothing here but our own infrastructure.
	return peer
}

func (r *resolver) BucketValue(req *http.Request) string {
	return bucketValue(r.ClientIP(req))
}

// bucketValue groups addresses into what counts as "one client".
//
// IPv4 is one address per host. IPv6 is not: a residential customer is
// routinely delegated a /64 or larger, so limiting a /128 would hand an
// attacker 2^64 free buckets and make the limiter ornamental. Grouping at /64
// treats one allocation as one client, and going broader would start bucketing
// unrelated customers together.
func bucketValue(addr netip.Addr) string {
	if !addr.IsValid() {
		return "unknown"
	}
	if addr.Is4() || addr.Is4In6() {
		return addr.Unmap().String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

// peerAddr is the TCP peer, the one value a client cannot forge.
func peerAddr(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		// Not every RemoteAddr carries a port (httptest, unix sockets).
		host = req.RemoteAddr
	}
	addr, err := parseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

// forwardedChain flattens every forwarding header into one ordered list,
// earliest hop first.
//
// Both header families are read, X-Forwarded-For first, because a proxy may
// emit either and some emit both. Go does not join repeated headers, so
// Header.Values must be walked — an attacker can send several X-Forwarded-For
// lines and a parser that reads only the first would miss the rest.
func forwardedChain(req *http.Request) []netip.Addr {
	var out []netip.Addr

	for _, header := range req.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(header, ",") {
			if addr, err := parseAddr(strings.TrimSpace(part)); err == nil {
				out = append(out, addr)
			}
		}
	}

	for _, header := range req.Header.Values("Forwarded") {
		entries, err := splitForwarded(header)
		if err != nil {
			// A malformed chain is a signal, not noise: drop the whole header
			// rather than trusting the half that happened to parse.
			continue
		}
		for _, entry := range entries {
			if addr, err := forwardedFor(entry); err == nil {
				out = append(out, addr)
			}
		}
	}

	return out
}

// forwardedFor pulls the "for" parameter out of one RFC 7239 record.
func forwardedFor(entry string) (netip.Addr, error) {
	for _, param := range strings.Split(entry, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "for") {
			continue
		}
		return parseAddr(strings.Trim(strings.TrimSpace(value), `"`))
	}
	return netip.Addr{}, fmt.Errorf("no for parameter")
}

// parseAddr accepts the several shapes a forwarded address arrives in:
// bare, bracketed IPv6, and either with a port.
func parseAddr(raw string) (netip.Addr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, fmt.Errorf("empty address")
	}

	// "unknown" and obfuscated identifiers like "_hidden" are legal in RFC 7239
	// and carry no address.
	if raw == "unknown" || strings.HasPrefix(raw, "_") {
		return netip.Addr{}, fmt.Errorf("non-address identifier %q", raw)
	}

	if addr, err := netip.ParseAddr(raw); err == nil {
		// Unmap so ::ffff:1.2.3.4 and 1.2.3.4 land in the same bucket rather
		// than depending on how the client happened to connect.
		return addr.Unmap(), nil
	}
	if ap, err := netip.ParseAddrPort(raw); err == nil {
		return ap.Addr().Unmap(), nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap(), nil
		}
	}
	return netip.Addr{}, fmt.Errorf("not an IP address: %q", raw)
}

// splitForwarded splits a Forwarded header into its comma-separated records,
// respecting quoted strings.
//
// Quoting matters: RFC 7239 permits for="[2001:db8::1]:8080", and a naive split
// on "," would tear an address apart mid-quote.
func splitForwarded(header string) ([]string, error) {
	var entries []string
	start := 0
	inQuotes := false
	escaped := false

	for i := 0; i < len(header); i++ {
		c := header[i]

		if inQuotes {
			if escaped {
				escaped = false
				continue
			}

			switch c {
			case '\\':
				escaped = true
			case '"':
				inQuotes = false
			}
			continue
		}

		switch c {
		case '"':
			inQuotes = true
		case ',':
			entries = append(entries, strings.TrimSpace(header[start:i]))
			start = i + 1
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("unterminated quoted string")
	}

	entries = append(entries, strings.TrimSpace(header[start:]))
	return entries, nil
}

// ParseTrustedProxies turns a comma-separated config value into prefixes.
// A bare address is accepted and treated as a single-host prefix.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(part); err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is neither an address nor a CIDR", part)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}
