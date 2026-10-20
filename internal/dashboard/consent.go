package dashboard

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"

	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

const maxConsentBody = 64 << 10

// consentGet reports the consent state, the detected agent stores and the CSRF
// token that the consent POST must echo.
func (s *Server) consentGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.config()
	if err != nil {
		log.Printf("dashboard: telemetry config: %v", err)
		writeErr(w, http.StatusInternalServerError, "telemetry config unreadable")
		return
	}
	st, err := s.led.store()
	if err != nil {
		log.Printf("dashboard: telemetry ledger: %v", err)
		writeErr(w, http.StatusInternalServerError, "telemetry ledger unavailable")
		return
	}
	out, err := s.settings(r.Context(), cfg, st)
	s.respond(w, out, err)
}

// consentPost writes telemetry.yaml from the user's choice. It needs a
// same-origin request and the CSRF token, so a page on another origin cannot
// grant itself capture.
func (s *Server) consentPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request refused")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) != 1 {
		writeErr(w, http.StatusForbidden, "missing or wrong X-CSRF-Token")
		return
	}
	var req report.ConsentRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsentBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	level := telemetry.Level(req.Capture)
	switch level {
	case telemetry.Off, telemetry.Metadata, telemetry.Bodies:
	default:
		writeErr(w, http.StatusBadRequest, "capture must be off, metadata or bodies")
		return
	}
	if req.Purge && level != telemetry.Off {
		writeErr(w, http.StatusBadRequest, "purge needs capture=off")
		return
	}
	clients, err := s.knownClients(req.Sessions)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.config()
	if err != nil {
		log.Printf("dashboard: telemetry config: %v", err)
		writeErr(w, http.StatusInternalServerError, "telemetry config unreadable")
		return
	}
	cfg.Capture = level
	cfg.Sessions = telemetry.SessionsConfig{Enabled: len(clients) > 0, Clients: clients}
	cfg.Consent = telemetry.Consent{
		GrantedAt: s.opts.Now().UTC(),
		GrantedBy: "dashboard",
		Version:   telemetry.ConsentVersion,
	}
	if err := telemetry.SaveConfig(s.opts.Home, cfg); err != nil {
		log.Printf("dashboard: save telemetry config: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not save telemetry config")
		return
	}
	if req.Purge {
		if err := s.led.purge(); err != nil {
			log.Printf("dashboard: purge ledger: %v", err)
			writeErr(w, http.StatusInternalServerError, "could not purge the ledger")
			return
		}
	}
	st, err := s.led.store()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "telemetry ledger unavailable")
		return
	}
	out, err := s.settings(r.Context(), cfg, st)
	s.respond(w, out, err)
}

// knownClients returns the sorted, de-duplicated client allowlist. An unknown
// client name is an error, not a silent drop.
func (s *Server) knownClients(in []string) ([]string, error) {
	known := map[string]bool{}
	for _, a := range s.opts.Adapters {
		known[a.Client()] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, c := range in {
		if !known[c] {
			return nil, fmt.Errorf("unknown client %q", c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Server) settings(ctx context.Context, cfg telemetry.Config, st telemetry.Store) (report.Settings, error) {
	stats, err := st.Stats(ctx)
	if err != nil {
		return report.Settings{}, err
	}
	out := report.Settings{
		Capture:        string(cfg.Capture),
		Clients:        []report.ClientStore{},
		RetentionDays:  cfg.RetentionDays,
		MaxBodyBytes:   cfg.MaxBodyBytes,
		GrantedBy:      cfg.Consent.GrantedBy,
		ConsentCurrent: cfg.Consent.Version == telemetry.ConsentVersion && !cfg.Consent.GrantedAt.IsZero(),
		ConfigPath:     telemetry.ConfigPath(s.opts.Home),
		DBPath:         telemetry.DBPath(s.opts.Home),
		DroppedEvents:  stats.DroppedEvents,
		CSRFToken:      s.csrf,
	}
	if out.Capture == "" {
		out.Capture = string(telemetry.Off)
	}
	if !cfg.Consent.GrantedAt.IsZero() {
		t := cfg.Consent.GrantedAt
		out.GrantedAt = &t
	}
	out.DBSizeBytes = stats.SizeBytes
	if out.DBSizeBytes == 0 {
		if fi, err := os.Stat(out.DBPath); err == nil {
			out.DBSizeBytes = fi.Size()
		}
	}
	for _, a := range s.opts.Adapters {
		granted := telemetry.SessionsGranted(cfg, a.Client())
		out.SessionsOn = out.SessionsOn || granted
		out.Clients = append(out.Clients, report.ClientStore{
			Client:  a.Client(),
			Found:   a.Detect(s.opts.UserHome),
			Roots:   a.Roots(s.opts.UserHome),
			Granted: granted,
		})
	}
	return out, nil
}

// sameOrigin accepts a request whose Origin (or, when absent, Referer) names
// the host the request was addressed to.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host
}
