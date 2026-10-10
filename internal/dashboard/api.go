package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"reflect"
	"strconv"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/metrics"
)

// reqCtx is what every report handler needs for one request.
type reqCtx struct {
	st   telemetry.Store
	cfg  telemetry.Config
	opts metrics.Options
}

// prepare loads the config and the ledger for one request and parses the
// shared filters. It writes the error response and returns false on failure.
func (s *Server) prepare(w http.ResponseWriter, r *http.Request) (reqCtx, bool) {
	cfg, err := s.config()
	if err != nil {
		log.Printf("dashboard: telemetry config: %v", err)
		writeErr(w, http.StatusInternalServerError, "telemetry config unreadable")
		return reqCtx{}, false
	}
	st, err := s.led.store()
	if err != nil {
		log.Printf("dashboard: telemetry ledger: %v", err)
		writeErr(w, http.StatusInternalServerError, "telemetry ledger unavailable")
		return reqCtx{}, false
	}
	q := r.URL.Query()
	since := s.opts.Since
	if v := q.Get("since"); v != "" {
		t, err := ParseSince(v, s.opts.Now())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return reqCtx{}, false
		}
		since = t
	}
	project := s.opts.Project
	if v := q.Get("project"); v != "" {
		project = v
	}
	return reqCtx{st: st, cfg: cfg, opts: metrics.Options{
		Since:   since,
		Client:  q.Get("client"),
		Project: project,
		Now:     s.opts.Now,
	}}, true
}

func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		log.Printf("dashboard: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, err := metrics.Overview(r.Context(), rc.st, rc.cfg, rc.opts)
	s.respond(w, out, err)
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	out, err := metrics.SessionList(r.Context(), rc.st, rc.opts, r.URL.Query().Get("outcome"), limit, offset)
	s.respond(w, out, err)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, found, err := metrics.SessionDetail(r.Context(), rc.st, r.PathValue("id"))
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) transcript(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, found, err := metrics.Transcript(r.Context(), rc.st, r.PathValue("id"), s.transcriptSource(rc.cfg))
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, found, err := metrics.CallDetail(r.Context(), rc.st, r.PathValue("id"))
	if err != nil {
		s.respond(w, nil, err)
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "call not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

var failureGroups = map[string]bool{"reason": true, "tool": true, "client": true}

func (s *Server) failures(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "reason"
	}
	if !failureGroups[group] {
		writeErr(w, http.StatusBadRequest, "group must be reason, tool or client")
		return
	}
	out, err := metrics.Failures(r.Context(), rc.st, rc.opts, group)
	s.respond(w, out, err)
}

func (s *Server) tools(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, err := metrics.Tools(r.Context(), rc.st, rc.opts)
	s.respond(w, out, err)
}

func (s *Server) memory(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, err := metrics.Memory(r.Context(), rc.st, rc.opts, r.URL.Query().Get("bank"))
	s.respond(w, out, err)
}

func (s *Server) evidence(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.prepare(w, r)
	if !ok {
		return
	}
	out, err := metrics.Evidence(r.Context(), rc.st, rc.opts, s.transcriptSource(rc.cfg))
	s.respond(w, out, err)
}

// transcriptSource returns the transcript reader for the current config, or
// nil when no client has a sessions grant. The reader returns nil turns for a
// session whose client is not granted or that has no transcript path.
func (s *Server) transcriptSource(cfg telemetry.Config) metrics.TranscriptSource {
	if !s.anyGranted(cfg) {
		return nil
	}
	return func(_ context.Context, sess telemetry.Session) ([]sessionlink.Turn, error) {
		if !telemetry.SessionsGranted(cfg, sess.ClientName) || sess.TranscriptPath == "" {
			return nil, nil
		}
		a := s.adapter(sess.ClientName)
		if a == nil {
			return nil, nil
		}
		return a.Transcript(sessionlink.TranscriptRef{
			Client:    sess.ClientName,
			Path:      telemetry.ExpandHome(sess.TranscriptPath), // stored redacted (~)
			SessionID: sess.ClientSessionID,
		}, maxTranscriptTurns)
	}
}

func paging(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit, offset = 50, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeErr(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return 0, 0, false
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "offset must be 0 or more")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeJSON encodes v with every nil slice written as [] (RS-09: no null lists).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(withEmptyLists(v))
}

// withEmptyLists returns a copy of v in which every nil slice reachable through
// exported fields is an empty slice.
func withEmptyLists(v any) any {
	if v == nil {
		return v
	}
	cp := reflect.New(reflect.TypeOf(v))
	cp.Elem().Set(reflect.ValueOf(v))
	fillEmptyLists(cp.Elem())
	return cp.Elem().Interface()
}

func fillEmptyLists(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).IsExported() {
				fillEmptyLists(v.Field(i))
			}
		}
	case reflect.Pointer:
		if !v.IsNil() {
			fillEmptyLists(v.Elem())
		}
	case reflect.Slice:
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			fillEmptyLists(v.Index(i))
		}
	}
}
