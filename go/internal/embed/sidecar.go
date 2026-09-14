package embed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// envOrSidecar reads an environment variable with a fallback.
func envOrSidecar(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Sidecar lifecycle for the local embedding provider (LEANKG_EMBED_PROVIDER=local).

// The Rust engine ran ONNX in-process; the Go engine instead drives an
// OpenAI-compatible llama.cpp `llama-server` sidecar, so heavy inference
// stays out of the serving binary. Ported behavior from
// .rustref/src/embeddings/provider.rs create_local_provider: a local
// provider that cannot be constructed fails hard with an actionable
// error — never a silent degrade to fake vectors.

// Defaults for the sidecar environment (see SidecarConfigFromEnv).
const (
	defaultSidecarCommand = "llama-server"
	defaultSidecarPort    = 8080
	defaultReadyTimeout   = 120 * time.Second
	defaultPollInterval   = 250 * time.Millisecond
	// shutdownGrace bounds the SIGTERM phase before the process group is
	// SIGKILLed.
	shutdownGrace = 3 * time.Second
	// stderrTail is how much of the sidecar's stderr is kept for errors.
	stderrTail = 8 << 10
)

// SidecarConfig describes the llama.cpp-compatible sidecar process.
type SidecarConfig struct {
	Command      string        // executable; default "llama-server"
	Args         []string      // extra argv; `--port N` appended when absent
	Port         int           // TCP port the sidecar must listen on
	Env          []string      // additional environment (parent env is inherited)
	ReadyTimeout time.Duration // health-poll bound; default 120s
	PollInterval time.Duration // health-poll cadence; default 250ms
}

// SidecarConfigFromEnv reads the sidecar environment:
//
//	LEANKG_EMBED_SIDECAR_CMD       executable (default llama-server)
//	LEANKG_EMBED_SIDECAR_ARGS      shell-quoted extra argv
//	LEANKG_EMBED_SIDECAR_PORT      port (default 8080)
//	LEANKG_EMBED_SIDECAR_READY_SECS  startup health-poll bound (default 120)
func SidecarConfigFromEnv() (SidecarConfig, error) {
	cfg := SidecarConfig{
		Command:      envOrSidecar("LEANKG_EMBED_SIDECAR_CMD", defaultSidecarCommand),
		Port:         defaultSidecarPort,
		ReadyTimeout: defaultReadyTimeout,
		PollInterval: defaultPollInterval,
	}
	args, err := splitArgs(os.Getenv("LEANKG_EMBED_SIDECAR_ARGS"))
	if err != nil {
		return cfg, fmt.Errorf("embed: invalid LEANKG_EMBED_SIDECAR_ARGS: %w", err)
	}
	cfg.Args = args
	if v := os.Getenv("LEANKG_EMBED_SIDECAR_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return cfg, fmt.Errorf("embed: invalid LEANKG_EMBED_SIDECAR_PORT %q", v)
		}
		cfg.Port = n
	}
	if v := os.Getenv("LEANKG_EMBED_SIDECAR_READY_SECS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("embed: invalid LEANKG_EMBED_SIDECAR_READY_SECS %q", v)
		}
		cfg.ReadyTimeout = time.Duration(n) * time.Second
	}
	return cfg, nil
}

// Sidecar is one running sidecar process. Shutdown is idempotent and
// releases the whole process group, so llama-server's children die with it.
type Sidecar struct {
	cmd     *exec.Cmd
	port    int
	stderr  *tailBuffer
	exited  chan struct{} // closed when Wait returns
	exitErr error         // valid after exited
	done    chan struct{} // closed when Shutdown completed
	stopOne sync.Once
}

// StartSidecar spawns the sidecar process and polls its /health endpoint
// until it reports ready (HTTP 200) or the startup budget is exhausted. The
// caller must invoke Shutdown (also triggered automatically when ctx is
// canceled). Startup failure always stops the process again before
// returning the error.
func StartSidecar(ctx context.Context, cfg SidecarConfig) (*Sidecar, error) {
	if cfg.Port == 0 {
		cfg.Port = defaultSidecarPort
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = defaultReadyTimeout
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}

	args := append([]string(nil), cfg.Args...)
	if !portFlagPresent(args) {
		args = append(args, "--port", strconv.Itoa(cfg.Port))
	}

	cmd := exec.Command(cfg.Command, args...)
	if cfg.Env != nil {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	// Own process group so Shutdown can kill the sidecar's children too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &tailBuffer{limit: stderrTail}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, sidecarStartError(cfg.Command, err)
	}
	s := &Sidecar{
		cmd:    cmd,
		port:   cfg.Port,
		stderr: stderr,
		exited: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		s.exitErr = cmd.Wait()
		close(s.exited)
	}()

	if err := awaitHealthy(ctx, s.healthURL(), cfg.ReadyTimeout, cfg.PollInterval, s.exited); err != nil {
		s.Shutdown()
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("embed: sidecar startup canceled: %w", ctx.Err())
		case errors.Is(err, errSidecarExited):
			return nil, fmt.Errorf("embed: sidecar %q exited during startup: %s", cfg.Command, s.exitDetail())
		default:
			return nil, fmt.Errorf("embed: sidecar %q not ready within %s (health %s); "+
				"check LEANKG_EMBED_SIDECAR_ARGS or attach to a running server via LEANKG_EMBED_BASE_URL",
				cfg.Command, cfg.ReadyTimeout, s.healthURL())
		}
	}
	// Context cancel shuts the sidecar down cleanly.
	go func() {
		select {
		case <-ctx.Done():
			s.Shutdown()
		case <-s.done:
		}
	}()
	return s, nil
}

// BaseURL is the OpenAI-compatible API root the provider plumbing expects,
// e.g. "http://127.0.0.1:8080/v1".
func (s *Sidecar) BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d/v1", s.port) }

func (s *Sidecar) healthURL() string { return fmt.Sprintf("http://127.0.0.1:%d/health", s.port) }

// Shutdown terminates the sidecar process group: SIGTERM first, SIGKILL
// after the grace period. Idempotent; safe to call from multiple goroutines.
func (s *Sidecar) Shutdown() error {
	s.stopOne.Do(func() {
		select {
		case <-s.exited: // died on its own; nothing to signal
		default:
			killGroup(s.cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-s.exited:
			case <-time.After(shutdownGrace):
				killGroup(s.cmd.Process.Pid, syscall.SIGKILL)
				<-s.exited
			}
		}
		close(s.done)
	})
	<-s.done
	return nil
}

// exitDetail renders the Wait result plus the captured stderr tail for
// startup-failure error messages.
func (s *Sidecar) exitDetail() string {
	detail := "unknown wait error"
	if ee, ok := s.exitErr.(*exec.ExitError); ok {
		detail = fmt.Sprintf("exit status %d", ee.ExitCode())
	} else if s.exitErr != nil {
		detail = s.exitErr.Error()
	}
	if msg := s.stderr.String(); msg != "" {
		detail += "; stderr: " + msg
	}
	return detail
}

func killGroup(pgid int, sig syscall.Signal) { _ = syscall.Kill(-pgid, sig) }

// portFlagPresent reports whether the user's own argv already pins a port
// (either "--port N" or "--port=N"); in that case we must not append ours.
func portFlagPresent(args []string) bool {
	for i, a := range args {
		if a == "--port" && i+1 < len(args) {
			return true
		}
		if strings.HasPrefix(a, "--port=") {
			return true
		}
	}
	return false
}

var errSidecarExited = errors.New("sidecar exited during startup")

// awaitHealthy polls the llama.cpp health endpoint: llama-server answers
// 503 while the model loads and 200 once ready.
func awaitHealthy(ctx context.Context, url string, timeout, interval time.Duration, exited <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return errSidecarExited
		case <-time.After(interval):
		}
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("health poll timed out")
		}
	}
}

func sidecarStartError(command string, err error) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
		return fmt.Errorf("embed: local provider requires the llama.cpp sidecar but %q was not found on PATH; "+
			"install llama-server, set LEANKG_EMBED_SIDECAR_CMD, or attach to a running server via LEANKG_EMBED_BASE_URL",
			command)
	}
	return fmt.Errorf("embed: cannot start sidecar %q: %w", command, err)
}

// tailBuffer keeps at most limit bytes, dropping the front — enough of a
// crashing sidecar's stderr to explain why.
type tailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimRight(string(t.buf), "\n")
}

// splitArgs splits a shell-quoted argument string the way /bin/sh would:
// whitespace separates words, single and double quotes group them, and a
// backslash escapes the next byte outside quotes (and inside double
// quotes). ponytail: hand-rolled because go.mod is frozen; upgrade to
// shellwords if escaping corner cases ever matter.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == 0 && (c == ' ' || c == '\t'):
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		case quote == 0 && (c == '\'' || c == '"'):
			quote, inWord = c, true
		case quote == '\'' && c == '\'':
			quote = 0
		case quote == '"' && c == '"':
			quote = 0
		case c == '\\' && quote != '\'' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
