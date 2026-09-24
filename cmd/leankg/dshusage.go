//go:build dshusage

package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/dsusage"
)

func cmdDSHUsage(args []string) {
	fs := flag.NewFlagSet("dsh-usage", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:9710", "dashboard listen address")
	roots := fs.String("sessions", "", "comma-separated DSH session roots (default ~/.dsh/sessions)")
	watch := fs.Bool("watch", false, "continuously scan sessions, classify, optionally score with Laya, notify")
	interval := fs.Duration("interval", 8*time.Second, "watch poll interval")
	// Laya is opt-in: empty default. Env LAYA_URL / LEANKG_JUDGE_SIDECAR_URL also enable it.
	layaURL := fs.String("laya-url", "", "Laya sidecar base URL (default off; or set LAYA_URL)")
	notify := fs.Bool("notify", true, "macOS notification on high/critical (watch mode)")
	dshURL := fs.String("dsh-url", envOrDSH("DSH_WEB_URL", "http://127.0.0.1:3081"), "DSH web origin for optional session inject")
	cookieFile := fs.String("dsh-cookie-file", "", "Netscape/raw cookie file with dsh-auth (enables in-session ask inject)")
	cookieRaw := fs.String("dsh-cookie", "", "raw Cookie header value (alternative to --dsh-cookie-file)")
	minSev := fs.String("min-severity", "high", "minimum severity to alert: critical|high|medium|info")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	var list []string
	for _, p := range strings.Split(*roots, ",") {
		if p = strings.TrimSpace(p); p != "" {
			list = append(list, p)
		}
	}
	var laya dsusage.LayaClient
	if u := strings.TrimSpace(*layaURL); u != "" {
		laya = dsusage.NewLaya(u)
	} else {
		laya = dsusage.DefaultLaya() // still honors LAYA_URL / LEANKG_JUDGE_SIDECAR_URL
	}
	layaOn := laya.Backend != nil
	findings := dsusage.NewFindingsStore("")

	if *cookieFile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if _, err := os.Stat(home + "/.leankg/dsh-cookies.txt"); err == nil {
				*cookieFile = home + "/.leankg/dsh-cookies.txt"
			}
		}
	}
	var w *dsusage.Watcher
	if *watch {
		cookie := strings.TrimSpace(*cookieRaw)
		if cookie == "" && *cookieFile != "" {
			c, err := dsusage.ReadCookieFile(*cookieFile)
			if err != nil {
				log.Printf("dsh-usage: cookie file: %v (asks stay on dashboard)", err)
			} else {
				cookie = c
			}
		}
		if cookie == "" {
			if c, err := dsusage.ReadCookieFile("/tmp/dsh-cookies.txt"); err == nil {
				cookie = c
				log.Printf("dsh-usage: using /tmp/dsh-cookies.txt for DSH inject")
			}
		}
		w = dsusage.NewWatcher(dsusage.WatchConfig{
			Roots:       list,
			Laya:        laya,
			Interval:    *interval,
			Notify:      *notify,
			DSHURL:      strings.TrimSpace(*dshURL),
			DSHCookie:   cookie,
			Findings:    findings,
			MinSeverity: dsusage.Severity(*minSev),
		})
		stop := make(chan struct{})
		go w.Loop(stop)
	}

	if !*watch {
		go func() {
			if _, err := findings.Scan(list); err != nil {
				log.Printf("dsh-usage initial scan: %v", err)
			}
		}()
	}

	h := dsusage.Handler(list, laya, w, findings)
	log.Printf("leankg dsh-usage on http://%s (watch=%v laya=%v findings=%s)", *addr, *watch, layaOn, findings.Path())
	if err := http.ListenAndServe(*addr, h); err != nil {
		log.Fatal(err)
	}
}

func envOrDSH(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
