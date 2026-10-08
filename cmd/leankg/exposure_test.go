package main

import "testing"

// TestExposedWithoutAuth pins the RS-03 warning rule.
func TestExposedWithoutAuth(t *testing.T) {
	for _, c := range []struct {
		addr   string
		gateOn bool
		want   bool
	}{
		{"127.0.0.1:9699", false, false},
		{"localhost:9699", false, false},
		{"[::1]:9699", false, false},
		{":9699", false, true},
		{"0.0.0.0:9699", false, true},
		{"192.168.1.5:9699", false, true},
		{":9699", true, false},
	} {
		if got := exposedWithoutAuth(c.addr, c.gateOn); got != c.want {
			t.Errorf("exposedWithoutAuth(%q, %v) = %v, want %v", c.addr, c.gateOn, got, c.want)
		}
	}
	if defaultHTTPAddr != "127.0.0.1:9699" {
		t.Fatalf("default MCP address %q is not loopback", defaultHTTPAddr)
	}
}
