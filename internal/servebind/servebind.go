// Package servebind holds the loopback-only policy for the `leankg serve`
// data listeners.
//
// A listener that leaves the loopback interface is refused unless the
// surface it serves is authenticated. The MCP, REST and ConnectRPC surfaces
// enforce the bearer gate (internal/auth), so a configured token — in the
// environment or in the token store — makes exposing them the operator's
// deliberate choice. With no token every caller resolves to Admin, so a
// wildcard bind hands the project graph, the file reader and the memory
// write path to every host that can route to this machine.
//
// The dashboard listener (UI) is deliberately NOT covered here: it is public
// by design — the Render demo serves `leankg serve --read-only --ui
// :${PORT}` to the public internet — so it keeps its own warning instead of
// a refusal.
//
// The policy lives here rather than in cmd/leankg so it can be unit-tested
// without starting a server, and so every listener is judged by one rule.
package servebind

import (
	"fmt"
	"net"
)

// Kind names a `leankg serve` listener.
type Kind string

const (
	// KindMCP is the MCP streamable-HTTP listener.
	KindMCP Kind = "MCP"
	// KindREST is the REST API listener.
	KindREST Kind = "REST"
	// KindRPC is the ConnectRPC listener.
	KindRPC Kind = "ConnectRPC"
)

// Options carries what the policy needs to know about the running server.
type Options struct {
	// GateOn is true when a bearer token is configured (environment or
	// token store): the MCP, REST and ConnectRPC surfaces then reject
	// every unauthenticated caller.
	GateOn bool
}

// IsLoopback reports whether addr binds the loopback interface only.
//
// "localhost" counts as loopback (it resolves there). An empty host
// (":9700") is the wildcard — every interface, LAN included — so it is NOT
// loopback. An address that cannot be split is not loopback either: an
// address we cannot parse is an address we cannot prove is local.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Exposed reports whether addr listens beyond loopback while no token gates
// its callers.
func Exposed(addr string, gateOn bool) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return !gateOn
}

// Check refuses a bind that would put an unauthenticated surface on the
// LAN. An empty addr means the listener is not started, and is always fine.
//
// The refusal names both ways out: bind a loopback address, or configure a
// token first.
func Check(kind Kind, addr string, opts Options) error {
	if addr == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid %s listen address %q: %w", kind, addr, err)
	}
	if IsLoopback(addr) {
		return nil
	}
	if opts.GateOn {
		return nil
	}
	return fmt.Errorf("refusing to serve %s on %s: no auth token is configured, so every caller would be Admin. %s, or configure a token first (`leankg auth token create`, or the LEANKG_TOKEN_ADMIN environment variable)",
		kind, addr, loopbackHint(addr))
}

// loopbackHint names the loopback address with the same port, so the
// operator can copy it without working out which port the listener had.
func loopbackHint(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "Bind a loopback address"
	}
	return fmt.Sprintf("Bind 127.0.0.1:%s", port)
}
