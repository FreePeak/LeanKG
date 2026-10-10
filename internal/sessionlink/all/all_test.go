package all

import "testing"

func TestAdaptersMatchClients(t *testing.T) {
	as := Adapters()
	if len(as) != len(Clients) {
		t.Fatalf("adapters %d != clients %d", len(as), len(Clients))
	}
	for i, a := range as {
		if a.Client() != Clients[i] {
			t.Fatalf("adapter %d client %q, want %q", i, a.Client(), Clients[i])
		}
		if _, ok := ByClient(Clients[i]); !ok {
			t.Fatalf("ByClient(%q) missing", Clients[i])
		}
	}
}
