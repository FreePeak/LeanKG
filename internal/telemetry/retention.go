package telemetry

import (
	"context"
	"sync"
	"time"
)

// retentionEvery is how often a running recorder sweeps old rows (DS-02).
const retentionEvery = 24 * time.Hour

// retentionSweepAt deletes rows older than days before now. A non-positive
// days value means the default window.
func retentionSweepAt(ctx context.Context, st Store, days int, now time.Time) (int64, error) {
	if days <= 0 {
		days = DefaultRetentionDays
	}
	return st.Purge(ctx, now.AddDate(0, 0, -days))
}

// startRetention sweeps once now and then daily until the returned stop
// function is called. Errors are dropped: retention must never affect a call.
// StartRetention is the exported form of startRetention: a caller that builds
// a recorder lazily (cmd/leankg's consent reload, when capture was off at
// startup) needs the same retention sweep the normal open path installs, or a
// first-after-reload ledger would never be swept.
func StartRetention(st Store, days int) (func(), error) {
	if st == nil {
		return func() {}, nil
	}
	return startRetention(st, days), nil
}

func startRetention(st Store, days int) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer func() { _ = recover() }()
		sweep := func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			_, _ = retentionSweepAt(ctx, st, days, time.Now())
		}
		sweep()
		t := time.NewTicker(retentionEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				sweep()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}
