package main

import "testing"

// TestCentralMemoryWired pins when serve hands agent_recall the central memory.
// With the gate on every caller must authenticate, so the central memory is
// wired whatever the bind. With the gate off every caller resolves to Admin, so
// the central memory is wired only while no listener binds beyond loopback. A
// stdio session starts no listener, so it is wired with the gate off.
func TestCentralMemoryWired(t *testing.T) {
	cases := []struct {
		name   string
		gateOn bool
		binds  []string
		want   bool
	}{
		{"loopback listeners, gate off", false, []string{"127.0.0.1:9699", "127.0.0.1:9700", "localhost:8081"}, true},
		{"no listener (stdio), gate off", false, nil, true},
		{"public dashboard, gate off", false, []string{"127.0.0.1:9699", ":8081"}, false},
		{"public REST, gate off", false, []string{"0.0.0.0:9700"}, false},
		{"public dashboard, gate on", true, []string{"127.0.0.1:9699", ":8081"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := centralMemoryWired(c.gateOn, c.binds); got != c.want {
				t.Errorf("centralMemoryWired(gateOn=%v, binds=%v) = %v, want %v", c.gateOn, c.binds, got, c.want)
			}
		})
	}
}
