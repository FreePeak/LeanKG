// Package dashboard serves the `leankg dashboard` web UI and its read API
// (/api/dashboard/v1, plan v4.15 DS-20, DS-21, DS-24). It is local-first:
// loopback only unless --allow-remote with a bearer token, and it reads only
// the telemetry ledger under its own LEANKG_HOME.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

const (
	maxTranscriptTurns = 2000
	linkEvery          = 60 * time.Second
	linkCheckEvery     = 5 * time.Second
)

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Options configures a Server.
type Options struct {
	Home        string    // LEANKG_HOME: the telemetry ledger and config
	Since       time.Time // default window start for every report; zero = all time
	Project     string    // default project filter
	Adapters    []sessionlink.Adapter
	UserHome    string           // real home where agent stores live; default os.UserHomeDir
	Token       string           // bearer token, required with AllowRemote
	AllowRemote bool             // require the bearer token on every request
	Now         func() time.Time // nil = time.Now

	openStore func(home string, readOnly bool) (telemetry.Store, error) // test seam
}

// Server is the dashboard HTTP handler plus its background linker.
type Server struct {
	opts Options
	led  *ledger
	csrf string
	h    http.Handler
}

// New validates opts and builds the handler. The ledger is not touched here.
func New(opts Options) (*Server, error) {
	if opts.Home == "" {
		return nil, errors.New("dashboard: home directory is required")
	}
	if opts.AllowRemote && opts.Token == "" {
		return nil, errors.New("dashboard: remote mode needs a bearer token")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.UserHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			opts.UserHome = h
		}
	}
	if opts.openStore == nil {
		opts.openStore = telemetry.OpenStore
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, csrf: csrf, led: &ledger{home: opts.Home, open: opts.openStore}}
	s.h = s.routes()
	return s, nil
}

// Handler is the full HTTP handler: security headers, optional bearer auth,
// the API and the embedded SPA.
func (s *Server) Handler() http.Handler { return s.h }

// Run keeps the transcript linker in step with the consent config until ctx
// is done, then closes the ledger. It starts linking only while a client has
// a sessions grant and a ledger exists.
func (s *Server) Run(ctx context.Context) {
	defer s.led.close()
	t := time.NewTicker(linkCheckEvery)
	defer t.Stop()
	for {
		s.reconcileLinker(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) reconcileLinker(ctx context.Context) {
	cfg, err := telemetry.LoadConfig(s.opts.Home)
	if err != nil {
		return
	}
	if !s.anyGranted(cfg) {
		s.led.stopLinker()
		return
	}
	if s.led.linking() {
		return
	}
	st, err := s.led.store()
	if err != nil {
		return
	}
	if _, empty := st.(emptyStore); empty {
		return
	}
	s.led.startLinker(ctx, func(lctx context.Context) {
		sessionlink.Loop(lctx, st, s.opts.UserHome, s.configFn(), s.opts.Adapters, linkEvery)
	})
}

func (s *Server) configFn() func() telemetry.Config {
	return func() telemetry.Config {
		cfg, err := telemetry.LoadConfig(s.opts.Home)
		if err != nil {
			return telemetry.Config{Capture: telemetry.Off}
		}
		return cfg
	}
}

func (s *Server) config() (telemetry.Config, error) { return telemetry.LoadConfig(s.opts.Home) }

func (s *Server) anyGranted(cfg telemetry.Config) bool {
	for _, a := range s.opts.Adapters {
		if telemetry.SessionsGranted(cfg, a.Client()) {
			return true
		}
	}
	return false
}

func (s *Server) adapter(client string) sessionlink.Adapter {
	for _, a := range s.opts.Adapters {
		if a.Client() == client {
			return a
		}
	}
	return nil
}

func (s *Server) routes() http.Handler {
	const p = "/api/dashboard/v1/"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+p+"overview", s.overview)
	mux.HandleFunc("GET "+p+"sessions", s.sessions)
	mux.HandleFunc("GET "+p+"sessions/{id}", s.session)
	mux.HandleFunc("GET "+p+"sessions/{id}/transcript", s.transcript)
	mux.HandleFunc("GET "+p+"calls/{id}", s.call)
	mux.HandleFunc("GET "+p+"failures", s.failures)
	mux.HandleFunc("GET "+p+"tools", s.tools)
	mux.HandleFunc("GET "+p+"memory", s.memory)
	mux.HandleFunc("GET "+p+"evidence", s.evidence)
	mux.HandleFunc("GET "+p+"consent", s.consentGet)
	mux.HandleFunc("POST "+p+"consent", s.consentPost)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", spaHandler())
	return s.secure(mux)
}

// secure sets the response headers and, in remote mode, requires the bearer
// token on every request (constant-time compare).
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.opts.AllowRemote && !s.bearerOK(r) {
			h.Set("WWW-Authenticate", `Bearer realm="leankg-dashboard"`)
			writeErr(w, http.StatusUnauthorized, "unauthorized: send Authorization: Bearer <token>")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bearerOK(r *http.Request) bool {
	const pfx = "Bearer "
	a := r.Header.Get("Authorization")
	if !strings.HasPrefix(a, pfx) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(a, pfx)), []byte(s.opts.Token)) == 1
}

// CheckListen refuses an address the dashboard must not serve. Only loopback
// is served unless allowRemote is set, and allowRemote needs a token.
func CheckListen(addr string, allowRemote bool, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	if !isLoopback(host) && !allowRemote {
		return fmt.Errorf("refusing to listen on %s: the dashboard is loopback-only; "+
			"pass --allow-remote with --token to expose it", addr)
	}
	if allowRemote && token == "" {
		return errors.New("--allow-remote needs --token: every request must carry a bearer token")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ParseSince reads a window such as 24h, 7d or 2w, or an RFC 3339 instant.
// An empty string means all time (zero time, no error).
func ParseSince(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if len(v) < 2 {
		return time.Time{}, fmt.Errorf("invalid window %q (expected 24h, 7d, 2w or RFC 3339)", v)
	}
	n, err := parsePositive(v[:len(v)-1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid window %q (expected 24h, 7d, 2w or RFC 3339)", v)
	}
	var unit time.Duration
	switch v[len(v)-1] {
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("invalid window %q (expected 24h, 7d, 2w or RFC 3339)", v)
	}
	return now.Add(-time.Duration(n) * unit), nil
}

func parsePositive(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, errors.New("too large")
		}
	}
	if n == 0 {
		return 0, errors.New("zero")
	}
	return n, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("dashboard: token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
