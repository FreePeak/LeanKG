package servebind

import (
	"strings"
	"testing"
)

// TestCheckRefusesExposedBindWithoutToken is the policy unit test: a
// non-loopback bind of a data listener is refused while no token gates its
// callers, and the refusal names both ways out.
func TestCheckRefusesExposedBindWithoutToken(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		addr string
	}{
		{KindMCP, ":9699"},
		{KindMCP, "0.0.0.0:9699"},
		{KindMCP, "192.168.1.5:9699"},
		{KindREST, ":9700"},
		{KindREST, "0.0.0.0:9700"},
		{KindREST, "192.168.1.5:9700"},
		{KindRPC, ":9090"},
		{KindRPC, "[::]:9090"},
	} {
		err := Check(c.kind, c.addr, Options{GateOn: false})
		if err == nil {
			t.Errorf("Check(%s, %q, gate off) = nil; want a refusal", c.kind, c.addr)
			continue
		}
		if !strings.Contains(err.Error(), "no auth token is configured") {
			t.Errorf("Check(%s, %q, gate off) = %q; want the missing-token reason", c.kind, c.addr, err)
		}
		if !strings.Contains(err.Error(), "leankg auth token create") {
			t.Errorf("Check(%s, %q, gate off) = %q; want the fix named", c.kind, c.addr, err)
		}
	}
}

// TestCheckAcceptsLoopbackBind is the other half: loopback is always fine,
// token or not, because a loopback listener is not reachable from the LAN.
func TestCheckAcceptsLoopbackBind(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:9700", "127.0.0.1:8080", "localhost:9700", "[::1]:9700"} {
		if err := Check(KindREST, addr, Options{GateOn: false}); err != nil {
			t.Errorf("Check(REST, %q, gate off) = %v; want nil", addr, err)
		}
	}
}

// TestCheckAcceptsExposedBindWithToken: a configured token means every
// caller must authenticate, so exposing the listener is the operator's
// deliberate choice.
func TestCheckAcceptsExposedBindWithToken(t *testing.T) {
	for _, kind := range []Kind{KindMCP, KindREST, KindRPC} {
		if err := Check(kind, "0.0.0.0:9700", Options{GateOn: true}); err != nil {
			t.Errorf("Check(%s, wildcard, gate on) = %v; want nil", kind, err)
		}
	}
}

// TestCheckSkipsUnstartedListener: an empty address means the listener is
// not started at all, so there is nothing to refuse.
func TestCheckSkipsUnstartedListener(t *testing.T) {
	if err := Check(KindREST, "", Options{GateOn: false}); err != nil {
		t.Errorf("Check(REST, \"\", gate off) = %v; want nil", err)
	}
}

// TestCheckRefusesInvalidAddress: an address that cannot be parsed is not a
// bind we can honour, so it is an error rather than a silent pass.
func TestCheckRefusesInvalidAddress(t *testing.T) {
	err := Check(KindREST, "not-an-address", Options{GateOn: true})
	if err == nil {
		t.Fatal("Check(REST, \"not-an-address\", gate on) = nil; want an error")
	}
	if !strings.Contains(err.Error(), "invalid REST listen address") {
		t.Errorf("Check(REST, \"not-an-address\") = %q; want the invalid-address reason", err)
	}
}

// TestIsLoopback pins the loopback predicate: the wildcard form is NOT
// loopback, which is the whole reason the wildcard bind is refused.
func TestIsLoopback(t *testing.T) {
	for _, c := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9700", true},
		{"127.0.0.53:9700", true},
		{"localhost:9700", true},
		{"[::1]:9700", true},
		{":9700", false},
		{"0.0.0.0:9700", false},
		{"[::]:9700", false},
		{"192.168.1.5:9700", false},
		{"", false},
	} {
		if got := IsLoopback(c.addr); got != c.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}
