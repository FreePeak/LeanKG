//go:build dshusage

package dsusage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ask is a pending "help the agent?" prompt for the human.
type Ask struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	SessionID string    `json:"session_id"`
	Workspace string    `json:"workspace"`
	CallID    string    `json:"call_id"`
	Tool      string    `json:"tool"`
	Severity  Severity  `json:"severity"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Fix       string    `json:"fix"`
	Rules     []string  `json:"rules"`
	Status    string    `json:"status"` // pending|helped|ignored|injected|failed
	Note      string    `json:"note,omitempty"`
}

// WatchConfig drives continuous scan + notify + optional DSH inject.
type WatchConfig struct {
	Roots       []string
	Laya        LayaClient
	Interval    time.Duration
	Notify      bool
	DSHURL      string // e.g. http://127.0.0.1:3081
	DSHCookie   string // raw Cookie header value
	StatePath   string // seen call ids
	MinSeverity Severity
	OnAsk       func(Ask)
}

// Watcher polls session logs, classifies, scores, notifies, and queues asks.
type Watcher struct {
	cfg   WatchConfig
	mu    sync.Mutex
	seen  map[string]struct{}
	asks  []Ask
	sessionIssues []SessionIssue
	stats struct {
		Scans   int `json:"scans"`
		New     int `json:"new_steps"`
		Alerts  int `json:"alerts"`
		Helped  int `json:"helped"`
		Ignored int `json:"ignored"`
	}
}

func NewWatcher(cfg WatchConfig) *Watcher {
	if cfg.Interval <= 0 {
		cfg.Interval = 8 * time.Second
	}
	if cfg.MinSeverity == "" {
		cfg.MinSeverity = SevHigh
	}
	if cfg.StatePath == "" {
		home, _ := os.UserHomeDir()
		cfg.StatePath = filepath.Join(home, ".leankg", "dsh-usage-seen.json")
	}
	w := &Watcher{cfg: cfg, seen: map[string]struct{}{}, asks: []Ask{}}
	w.loadSeen()
	return w
}

func stepKey(s Step) string {
	if s.CallID != "" {
		return s.SessionID + "|" + s.CallID
	}
	return fmt.Sprintf("%s|%d|%s", s.SessionID, s.TimeMS, s.Tool)
}

func (w *Watcher) loadSeen() {
	b, err := os.ReadFile(w.cfg.StatePath)
	if err != nil {
		return
	}
	var keys []string
	if json.Unmarshal(b, &keys) != nil {
		return
	}
	for _, k := range keys {
		w.seen[k] = struct{}{}
	}
}

func (w *Watcher) saveSeen() {
	w.mu.Lock()
	keys := make([]string, 0, len(w.seen))
	for k := range w.seen {
		keys = append(keys, k)
	}
	w.mu.Unlock()
	// keep last 5k
	if len(keys) > 5000 {
		keys = keys[len(keys)-5000:]
	}
	b, _ := json.Marshal(keys)
	_ = os.MkdirAll(filepath.Dir(w.cfg.StatePath), 0o755)
	_ = os.WriteFile(w.cfg.StatePath, b, 0o644)
}

// Snapshot returns asks + stats for the API.
func (w *Watcher) Snapshot() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	asks := append([]Ask(nil), w.asks...)
	si := append([]SessionIssue(nil), w.sessionIssues...)
	return map[string]any{
		"asks":           asks,
		"stats":          w.stats,
		"seen":           len(w.seen),
		"session_issues": si,
	}
}

// AnswerAsk records the human choice and optionally injects a DSH prompt.
func (w *Watcher) AnswerAsk(id string, help bool) (Ask, error) {
	w.mu.Lock()
	var a *Ask
	for i := range w.asks {
		if w.asks[i].ID == id {
			a = &w.asks[i]
			break
		}
	}
	if a == nil {
		w.mu.Unlock()
		return Ask{}, fmt.Errorf("ask %s not found", id)
	}
	if a.Status != "pending" {
		cp := *a
		w.mu.Unlock()
		return cp, nil
	}
	if !help {
		a.Status = "ignored"
		w.stats.Ignored++
		cp := *a
		w.mu.Unlock()
		return cp, nil
	}
	a.Status = "helped"
	w.stats.Helped++
	cp := *a
	w.mu.Unlock()

	if w.cfg.DSHURL != "" && w.cfg.DSHCookie != "" {
		msg := helpMessage(cp)
		if err := injectDSHPrompt(w.cfg.DSHURL, w.cfg.DSHCookie, cp.SessionID, msg); err != nil {
			w.mu.Lock()
			for i := range w.asks {
				if w.asks[i].ID == id {
					w.asks[i].Status = "failed"
					w.asks[i].Note = err.Error()
					cp = w.asks[i]
					break
				}
			}
			w.mu.Unlock()
			return cp, err
		}
		w.mu.Lock()
		for i := range w.asks {
			if w.asks[i].ID == id {
				w.asks[i].Status = "injected"
				w.asks[i].Note = "queued on session via session/prompt"
				cp = w.asks[i]
				break
			}
		}
		w.mu.Unlock()
	} else {
		w.mu.Lock()
		for i := range w.asks {
			if w.asks[i].ID == id {
				w.asks[i].Note = "no DSH cookie — open the session and paste the fix yourself"
				cp = w.asks[i]
				break
			}
		}
		w.mu.Unlock()
	}
	return cp, nil
}

func helpMessage(a Ask) string {
	return fmt.Sprintf(`[Laya/LeanKG watch] Session went wrong. Please help the agent.

Severity: %s
Tool: %s
Workspace: %s
Problem: %s
Detail: %s
Fix: %s
Rules: %s

Do this now:
1) Prefer mcp__leankg__query / status / import over bash/grep for code search.
2) Always pass project=<repo basename> or an absolute path.
3) If the store is cold, import once then query.
4) Do not treat LeanKG source as the answer when the user is working in another repo.
`, a.Severity, a.Tool, a.Workspace, a.Title, a.Detail, a.Fix, strings.Join(a.Rules, ", "))
}

// RunOnce scans and processes new steps. Returns new alert count.
func (w *Watcher) RunOnce() (int, error) {
	steps, si, err := ScanRoots(w.cfg.Roots)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.sessionIssues = si
	w.stats.Scans++
	w.mu.Unlock()

	var fresh []Step
	for _, s := range steps {
		s.Issues = Classify(s)
		s.TopSeverity = Top(s.Issues)
		k := stepKey(s)
		w.mu.Lock()
		_, ok := w.seen[k]
		if !ok {
			w.seen[k] = struct{}{}
			w.stats.New++
			fresh = append(fresh, s)
		}
		w.mu.Unlock()
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	// score only alert-worthy
	var toScore []Step
	for _, s := range fresh {
		if severityAtLeast(s.TopSeverity, w.cfg.MinSeverity) {
			toScore = append(toScore, s)
		}
	}
	if len(toScore) > 0 {
		ApplyLaya(toScore, w.cfg.Laya, len(toScore))
	}
	// map scores back by call id
	scored := map[string]Step{}
	for _, s := range toScore {
		scored[stepKey(s)] = s
	}
	alerts := 0
	for _, s := range fresh {
		if sc, ok := scored[stepKey(s)]; ok {
			s = sc
		}
		if !severityAtLeast(s.TopSeverity, w.cfg.MinSeverity) {
			continue
		}
		alerts++
		a := askFromStep(s)
		w.mu.Lock()
		if !w.hasAskFor(stepKey(s)) {
			w.asks = append([]Ask{a}, w.asks...)
			if len(w.asks) > 200 {
				w.asks = w.asks[:200]
			}
			w.stats.Alerts++
		}
		w.mu.Unlock()
		if w.cfg.Notify {
			notifyMac(a)
		}
		if w.cfg.OnAsk != nil {
			w.cfg.OnAsk(a)
		}
		log.Printf("dsh-usage alert %s %s %s %s", a.Severity, a.SessionID, a.Tool, a.Title)
	}
	w.saveSeen()
	return alerts, nil
}

// hasAskFor reports whether an ask already exists for this step key.
// Callers hold w.mu.
func (w *Watcher) hasAskFor(key string) bool {
	for _, a := range w.asks {
		if askKey(a) == key {
			return true
		}
	}
	return false
}

// askKey is the dedupe identity: same session + call, stable across restarts.
func askKey(a Ask) string {
	if a.CallID != "" {
		return a.SessionID + "|" + a.CallID
	}
	return a.SessionID + "|" + a.Tool + "|" + a.CreatedAt.Format("20060102T150405")
}

// Loop runs until ctx-less forever; caller cancels via process.
func (w *Watcher) Loop(stop <-chan struct{}) {
	steps, _, err := ScanRoots(w.cfg.Roots)
	if err != nil {
		return
	}
	w.mu.Lock()
	backlog := 0
	for _, s := range steps {
		s.Issues = Classify(s)
		s.TopSeverity = Top(s.Issues)
		w.seen[stepKey(s)] = struct{}{}
		// Surface existing high/critical once so the human can answer Help/Ignore.
		if severityAtLeast(s.TopSeverity, w.cfg.MinSeverity) && backlog < 12 && !w.hasAskFor(stepKey(s)) {
			a := askFromStep(s)
			a.Note = "backlog from existing session logs"
			w.asks = append(w.asks, a)
			w.stats.Alerts++
			backlog++
			if w.cfg.Notify && backlog <= 3 {
				go notifyMac(a)
			}
		}
	}
	nSeen := len(w.seen)
	w.mu.Unlock()
	w.saveSeen()
	log.Printf("dsh-usage watch: seeded %d steps, backlog asks=%d; interval=%s", nSeen, backlog, w.cfg.Interval)

	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if _, err := w.RunOnce(); err != nil {
				log.Printf("dsh-usage watch scan: %v", err)
			}
		}
	}
}

func askFromStep(s Step) Ask {
	title, detail, fix := "Session issue", "", ""
	var rules []string
	for _, i := range s.Issues {
		rules = append(rules, i.Rule)
		if i.Severity == s.TopSeverity && title == "Session issue" {
			title, detail, fix = i.Title, i.Detail, i.Fix
		}
	}
	return Ask{
		ID:        fmt.Sprintf("%s-%d", s.CallID, time.Now().UnixNano()),
		CreatedAt: time.Now().UTC(),
		SessionID: s.SessionID,
		Workspace: s.Workspace,
		CallID:    s.CallID,
		Tool:      s.Tool,
		Severity:  s.TopSeverity,
		Title:     title,
		Detail:    detail,
		Fix:       fix,
		Rules:     rules,
		Status:    "pending",
	}
}

func severityAtLeast(have, want Severity) bool {
	return sevRank(have) >= sevRank(want)
}

func sevRank(s Severity) int {
	switch s {
	case SevCritical:
		return 4
	case SevHigh:
		return 3
	case SevMedium:
		return 2
	case SevInfo:
		return 1
	default:
		return 0
	}
}

func notifyMac(a Ask) {
	title := "LeanKG DSH · " + string(a.Severity)
	body := a.Title + " — help the agent? Open http://127.0.0.1:9710"
	script := fmt.Sprintf(`display notification %q with title %q`, body, title)
	_ = exec.Command("osascript", "-e", script).Run()
}

// injectDSHPrompt queues a user message on a live DSH session.
func injectDSHPrompt(baseURL, cookie, sessionID, text string) error {
	baseURL = strings.TrimRight(baseURL, "/")
	body := map[string]any{
		"type":   "client-request",
		"rpcId":  "laya-watch-" + fmt.Sprint(time.Now().UnixNano()),
		"method": "session/prompt",
		"payload": map[string]any{
			"args": map[string]any{
				"request": map[string]any{
					"requestId": "laya-help-" + fmt.Sprint(time.Now().UnixNano()),
					"sessionId": sessionID,
					"mode":      "queue",
					"content":   []map[string]string{{"type": "text", "text": text}},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/session/prompt", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var raw struct {
		Result struct {
			OK    bool `json:"ok"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"result"`
	}
	_ = json.NewDecoder(res.Body).Decode(&raw)
	if res.StatusCode == 401 {
		return fmt.Errorf("dsh unauthorized — refresh cookie")
	}
	if !raw.Result.OK {
		msg := res.Status
		if raw.Result.Error != nil {
			msg = raw.Result.Error.Message
		}
		return fmt.Errorf("session/prompt: %s", msg)
	}
	return nil
}

// ReadCookieFile loads a Netscape or raw cookie file into a Cookie header value.
func ReadCookieFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("empty cookie file")
	}
	if !strings.Contains(s, "\t") && strings.Contains(s, "=") {
		// already name=value
		return strings.Split(s, "\n")[0], nil
	}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "#") && !strings.HasPrefix(ln, "#HttpOnly_") {
			continue
		}
		ln = strings.TrimPrefix(ln, "#")
		p := strings.Split(ln, "\t")
		if len(p) >= 7 && strings.Contains(p[5], "dsh-auth") {
			return p[5] + "=" + p[6], nil
		}
	}
	return "", fmt.Errorf("no dsh-auth cookie in %s", path)
}
