//go:build dshusage

package dsusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const findingsStoreVersion = 1

// ScanStatus lets the UI distinguish a cold scan from an empty result. A
// watcher sets scanning before it starts walking session logs, so the API
// never has to block a request behind that walk.
type ScanStatus struct {
	State         string `json:"state"` // idle | scanning | ready | error
	StartedAt     string `json:"started_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	Files         int    `json:"files"`
	Steps         int    `json:"steps"`
	SessionIssues int    `json:"session_issues"`
	Findings      int    `json:"findings"`
	LastError     string `json:"last_error,omitempty"`
}

// Finding is the small, agent-facing projection of a step or session issue.
// Full step text remains available from /api/steps; this shape intentionally
// keeps the common triage path cheap and actionable.
type Finding struct {
	Status     string   `json:"status"`
	Source     string   `json:"source"` // step | session
	Rule       string   `json:"rule"`
	Severity   Severity `json:"severity"`
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	Fix        string   `json:"fix"`
	SessionID  string   `json:"session_id,omitempty"`
	Workspace  string   `json:"workspace,omitempty"`
	CallID     string   `json:"call_id,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	TimeMS     int64    `json:"time_ms,omitempty"`
	Rung       string   `json:"rung,omitempty"`
	RungReason string   `json:"rung_reason,omitempty"`
	Input      string   `json:"input,omitempty"`
	Output     string   `json:"output,omitempty"`
}

// Snapshot is the in-memory view shared by the dashboard and REST findings
// endpoint. It is returned as a copy so Laya scoring or a UI caller cannot
// mutate the persisted cache.
type Snapshot struct {
	Steps         []Step         `json:"steps"`
	SessionIssues []SessionIssue `json:"session_issues"`
	Findings      []Finding      `json:"findings"`
	Scan          ScanStatus     `json:"scan"`
}

type fileFingerprint struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mod_time_unix_nano"`
}

type cachedLog struct {
	Fingerprint   fileFingerprint `json:"fingerprint"`
	Steps         []Step          `json:"steps"`
	SessionIssues []SessionIssue  `json:"session_issues"`
}

type persistedFindings struct {
	Version  int                  `json:"version"`
	Status   ScanStatus           `json:"status"`
	Files    map[string]cachedLog `json:"files"`
	Findings []Finding            `json:"findings"`
}

// FindingsStore is an additive JSON cache. It is deliberately separate from
// seen.json: seen.json is alert de-duplication state, while this file holds
// the evidence needed to render the dashboard and hand work to an agent.
type FindingsStore struct {
	path string

	scanMu sync.Mutex
	mu     sync.RWMutex
	data   persistedFindings
	status ScanStatus
}

// NewFindingsStore opens the optional persistent findings file. A missing or
// stale file is not fatal; the next watcher scan will create/repair it.
func NewFindingsStore(path string) *FindingsStore {
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, ".leankg", "dsh-usage-findings.json")
		} else {
			path = ".dsh-usage-findings.json"
		}
	}
	s := &FindingsStore{path: path, status: ScanStatus{State: "idle"}}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.status.LastError = err.Error()
		}
		return s
	}
	var p persistedFindings
	if err := json.Unmarshal(b, &p); err != nil || p.Version != findingsStoreVersion {
		if err == nil {
			err = fmt.Errorf("unsupported findings cache version %d", p.Version)
		}
		s.status.LastError = err.Error()
		return s
	}
	s.data = p
	s.status = p.Status
	// A process may have exited during a scan. The complete cache is still
	// usable, so do not leave the UI stuck in "scanning" after a restart.
	if s.status.State == "" || s.status.State == "scanning" {
		s.status.State = "ready"
	}
	return s
}

// Path returns the local cache path for diagnostics and agent instructions.
func (s *FindingsStore) Path() string { return s.path }

// Scan refreshes only files whose size or mtime changed. The directory walk is
// cheap; unchanged compressed logs are not opened, so the usual dashboard
// request does not spawn a zstd process for the whole session corpus.
//
// ponytail: an actively appended log is reparsed from the beginning on each
// poll. The known ceiling is one active file per session; if that becomes
// expensive, add a line-offset tail index without changing the cache schema.
func (s *FindingsStore) Scan(roots []string) (Snapshot, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	roots, err := defaultRoots(roots)
	if err != nil {
		return Snapshot{}, err
	}
	started := time.Now().UTC()
	s.mu.Lock()
	s.status.State = "scanning"
	s.status.StartedAt = started.Format(time.RFC3339Nano)
	s.status.LastError = ""
	s.mu.Unlock()

	selected, selectErr := selectedLogs(roots)
	if selectErr != nil {
		s.mu.Lock()
		// A missing default directory is a legitimate empty install. Once
		// evidence exists, however, a disappeared/unreadable root must not
		// silently erase the last good cache.
		if !(os.IsNotExist(selectErr) && len(s.data.Files) == 0) {
			s.status.State = "error"
			s.status.LastError = selectErr.Error()
			s.mu.Unlock()
			return s.Snapshot(), selectErr
		}
		selected = map[string]os.FileInfo{}
	}
	next := make(map[string]cachedLog, len(selected))
	var readErrors []string
	for path, info := range selected {
		fp := fingerprint(info)
		s.mu.RLock()
		cached, ok := s.data.Files[path]
		s.mu.RUnlock()
		if ok && cached.Fingerprint == fp {
			next[path] = cached
			continue
		}
		steps, issues, scanErr := scanFile(path)
		if scanErr != nil {
			// Keep the last good evidence for a transient read/decompression
			// failure rather than deleting a useful finding from the UI.
			if ok {
				next[path] = cached
			}
			readErrors = append(readErrors, fmt.Sprintf("%s: %v", path, scanErr))
			continue
		}
		next[path] = cachedLog{Fingerprint: fp, Steps: steps, SessionIssues: issues}
	}

	status := ScanStatus{
		State:      "ready",
		StartedAt:  started.Format(time.RFC3339Nano),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		DurationMS: time.Since(started).Milliseconds(),
		Files:      len(next),
		LastError:  strings.Join(readErrors, "; "),
	}
	snap := snapshotFrom(next, status)
	status = snap.Scan
	p := persistedFindings{Version: findingsStoreVersion, Status: status, Files: next, Findings: snap.Findings}
	if err := writeFindings(s.path, p); err != nil {
		s.mu.Lock()
		s.status.State = "error"
		s.status.LastError = err.Error()
		s.mu.Unlock()
		return s.Snapshot(), err
	}

	s.mu.Lock()
	s.data = p
	s.status = status
	s.mu.Unlock()
	return s.Snapshot(), nil
}

// Snapshot returns a defensive copy of the last completed scan.
func (s *FindingsStore) Snapshot() Snapshot {
	s.mu.RLock()
	p := s.data
	status := s.status
	s.mu.RUnlock()

	paths := make([]string, 0, len(p.Files))
	for path := range p.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	steps := make([]Step, 0)
	issues := make([]SessionIssue, 0)
	for _, path := range paths {
		record := p.Files[path]
		for _, step := range record.Steps {
			steps = append(steps, cloneStep(step))
		}
		issues = append(issues, record.SessionIssues...)
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].TimeMS != steps[j].TimeMS {
			return steps[i].TimeMS > steps[j].TimeMS
		}
		return steps[i].SessionID < steps[j].SessionID
	})
	sort.SliceStable(issues, func(i, j int) bool {
		if sevRank(issues[i].Severity) != sevRank(issues[j].Severity) {
			return sevRank(issues[i].Severity) > sevRank(issues[j].Severity)
		}
		return issues[i].Detail < issues[j].Detail
	})

	findings := cloneFindings(p.Findings)
	if findings == nil {
		findings = buildFindings(steps, issues)
	}
	status.Files = len(p.Files)
	status.Steps = len(steps)
	status.SessionIssues = len(issues)
	status.Findings = len(findings)
	if status.State == "" {
		status.State = "idle"
	}
	return Snapshot{Steps: steps, SessionIssues: issues, Findings: findings, Scan: status}
}

func snapshotFrom(files map[string]cachedLog, status ScanStatus) Snapshot {
	// Use Snapshot's same ordering/copy rules without taking the store lock.
	// This helper is used only before the new records are published.
	old := &FindingsStore{data: persistedFindings{Files: files}, status: status}
	return old.Snapshot()
}

func fingerprint(info os.FileInfo) fileFingerprint {
	return fileFingerprint{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

func defaultRoots(roots []string) ([]string, error) {
	if len(roots) > 0 {
		return roots, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []string{filepath.Join(home, ".dsh", "sessions")}, nil
}

// selectedLogs returns the same v4-over-v3 choice as ScanRoots, but without
// opening any log. The second walk is what supplies cheap file fingerprints.
func selectedLogs(roots []string) (map[string]os.FileInfo, error) {
	selected := map[string]os.FileInfo{}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			return nil, err
		}
		sessionsWithV4 := map[string]bool{}
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d == nil || !d.IsDir() || !strings.HasPrefix(d.Name(), "session-") {
				return nil
			}
			has, err := hasLogFile(path, "session.v4.jsonl")
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if has {
				sessionsWithV4[path] = true
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d == nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			isV4 := strings.HasPrefix(name, "session.v4.jsonl")
			isV3 := strings.HasPrefix(name, "session.v3.jsonl")
			if !isV4 && !isV3 {
				return nil
			}
			if isV3 && sessionsWithV4[filepath.Dir(path)] {
				return nil
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			selected[path] = info
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

func buildFindings(steps []Step, issues []SessionIssue) []Finding {
	findings := make([]Finding, 0)
	for _, step := range steps {
		for _, issue := range step.Issues {
			if issue.Severity != SevCritical && issue.Severity != SevHigh && issue.Severity != SevMedium {
				continue
			}
			findings = append(findings, Finding{
				Status: "open", Source: "step", Rule: issue.Rule, Severity: issue.Severity,
				Title: issue.Title, Detail: issue.Detail, Fix: issue.Fix,
				SessionID: step.SessionID, Workspace: step.Workspace, CallID: step.CallID,
				Tool: step.Tool, TimeMS: step.TimeMS, Rung: step.Rung,
				RungReason: clip(step.RungReason, 500), Input: clip(step.Input, 1000),
				Output: clip(step.Output, 1000),
			})
		}
	}
	for _, issue := range issues {
		if issue.Severity != SevCritical && issue.Severity != SevHigh && issue.Severity != SevMedium {
			continue
		}
		findings = append(findings, Finding{
			Status: "open", Source: "session", Rule: issue.Rule, Severity: issue.Severity,
			Title: issue.Title, Detail: issue.Detail, Fix: issue.Fix,
			SessionID: issue.SessionID, Workspace: issue.Workspace,
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if sevRank(findings[i].Severity) != sevRank(findings[j].Severity) {
			return sevRank(findings[i].Severity) > sevRank(findings[j].Severity)
		}
		if findings[i].Source != findings[j].Source {
			return findings[i].Source == "step"
		}
		if findings[i].TimeMS != findings[j].TimeMS {
			return findings[i].TimeMS > findings[j].TimeMS
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings
}

func cloneStep(step Step) Step {
	step.Issues = append([]Issue(nil), step.Issues...)
	return step
}

func cloneFindings(findings []Finding) []Finding {
	if findings == nil {
		return nil
	}
	return append([]Finding{}, findings...)
}

func writeFindings(path string, p persistedFindings) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dsh-usage-findings-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
