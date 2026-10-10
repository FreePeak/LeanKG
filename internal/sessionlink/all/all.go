// Package all registers every sessionlink adapter (plan v4.15 DS-11..DS-15).
// It is separate from package sessionlink because the adapters import it.
package all

import (
	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
	"github.com/FreePeak/LeanKG/internal/sessionlink/codex"
	"github.com/FreePeak/LeanKG/internal/sessionlink/gemini"
	"github.com/FreePeak/LeanKG/internal/sessionlink/grok"
	"github.com/FreePeak/LeanKG/internal/sessionlink/opencode"
	"github.com/FreePeak/LeanKG/internal/sessionlink/pifamily"
)

// Clients is the canonical client order used by the consent screen and CLI.
var Clients = []string{"claude-code", "xdev", "omp", "pi", "opencode", "grok", "codex", "gemini"}

// Adapters returns one adapter per supported client, in Clients order.
func Adapters() []sessionlink.Adapter {
	return []sessionlink.Adapter{
		claudecode.New(),
		pifamily.New("xdev"),
		pifamily.New("omp"),
		pifamily.New("pi"),
		opencode.New(),
		grok.New(),
		codex.New(),
		gemini.New(),
	}
}

// ByClient finds the adapter for a canonical client name.
func ByClient(client string) (sessionlink.Adapter, bool) {
	for _, a := range Adapters() {
		if a.Client() == client {
			return a, true
		}
	}
	return nil, false
}
