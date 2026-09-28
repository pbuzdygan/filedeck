package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
)

type clientKey struct{}

// clientAddr returns the address of the browser. Behind the trusted proxy it
// walks X-Forwarded-For from the right, skipping hops inside the proxy CIDR:
// entries a client wrote itself sit further left and are never reached while
// the proxy appends the address it saw. The result is used only for rate
// limits and logs, never for authentication or authorization.
func (a *API) clientAddr(peer netip.Addr, r *http.Request) netip.Addr {
	if !a.proxy.IsValid() || !a.proxy.Contains(peer) {
		return peer
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	client := peer
	for i := len(hops) - 1; i >= 0 && a.proxy.Contains(client); i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		client = ip.Unmap()
	}
	return client
}

// clientIP is the browser address stored by ServeHTTP.
func clientIP(r *http.Request) netip.Addr {
	ip, _ := r.Context().Value(clientKey{}).(netip.Addr)
	return ip
}

// limitKey groups IPv6 clients by /64, the block one subscriber usually gets,
// so rotating addresses inside it does not bypass rate limits.
func limitKey(ip netip.Addr) string {
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return p.String()
	}
	return ip.String()
}

func withClient(r *http.Request, ip netip.Addr) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), clientKey{}, ip))
}

// event writes one security log line (sign-ins, failures, account and link
// changes) with the client address, for monitoring and tools such as
// fail2ban or CrowdSec. Passwords, codes and tokens are never logged.
func (a *API) event(r *http.Request, level slog.Level, name string, attrs ...any) {
	ip := clientIP(r)
	a.log.Log(r.Context(), level, name, append([]any{"client", ip.String()}, attrs...)...)
}

// short caps user-supplied values (such as a typed username) in log lines.
func short(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}
