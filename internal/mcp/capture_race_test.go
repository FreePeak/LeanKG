package mcp

import (
	"sync"
	"testing"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// TestSetRecorderIsRaceFree pins CX-07 residual (1): a consent reload swaps
// the recorder while request goroutines read it. Run with -race, the base code
// reports a DATA RACE on Server.rec.
func TestSetRecorderIsRaceFree(t *testing.T) {
	srv := &Server{rec: telemetry.Nop{}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			srv.SetRecorder(telemetry.Nop{})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = srv.recorder().Level()
		}
	}()
	wg.Wait()
}
