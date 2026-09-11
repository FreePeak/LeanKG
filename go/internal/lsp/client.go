package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

// Typed errors returned by the package. Callers should use errors.Is.
var (
	ErrNoServer = errors.New("lsp: no server")
	ErrTimeout  = errors.New("lsp: timeout")
	ErrProtocol = errors.New("lsp: protocol error")
)

// handshakeTimeout bounds the initialize/initialized exchange. It is a var
// so tests can shrink it; 10s (raised from 5s) absorbs `go test` parallel
// package load where a local sh-spawn fake server was observed to miss the
// old 5s bound intermittently (3x gate flake, TestStartHandshakeAndClose).
var handshakeTimeout = 10 * time.Second

// Symbol is a normalized document or workspace symbol. Lines are 1-based
// (LSP's 0-based line numbers are shifted on conversion); a hierarchical
// child is flattened into the slice that contains it.
type Symbol struct {
	Name      string
	Kind      int // LSP SymbolKind
	StartLine int
	EndLine   int
}

// Available reports whether any of spec's command candidates resolves on
// PATH, i.e. whether Start could spawn a server. A nil spec is never
// available.
func Available(spec *langs.LSPSpec) bool {
	if spec == nil {
		return false
	}
	for _, cmd := range spec.Commands {
		if _, err := exec.LookPath(cmd); err == nil {
			return true
		}
	}
	return false
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client is a live LSP server process speaking JSON-RPC 2.0 over stdio with
// Content-Length framing. It is safe for concurrent use; requests are
// correlated by id and answered by a single reader goroutine.
type Client struct {
	lang    langs.Language
	rootDir string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	out     *bufio.Reader

	mu         sync.Mutex
	pending    map[int64]chan rpcResult
	nextID     int64
	broken     error
	readerDone chan struct{}

	lastUseMu sync.Mutex
	lastUse   time.Time

	closeOnce sync.Once
}

// Start launches the first resolvable server command from spec, performs the
// initialize handshake, and returns a live client. The handshake is bounded
// by handshakeTimeout (or ctx, whichever expires first); on any failure the
// spawned process is killed and reaped before Start returns.
func Start(ctx context.Context, lang langs.Language, spec *langs.LSPSpec, rootDir string) (*Client, error) {
	if spec == nil || len(spec.Commands) == 0 {
		return nil, fmt.Errorf("%w: no server commands configured for %s", ErrNoServer, lang)
	}
	var bin string
	for _, cmd := range spec.Commands {
		if p, err := exec.LookPath(cmd); err == nil {
			bin = p
			break
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("%w: none of the %d server commands for %s resolve on PATH",
			ErrNoServer, len(spec.Commands), lang)
	}

	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin)
	cmd.Dir = abs
	cmd.Stderr = io.Discard
	// A server that spawns children inheriting stdout would hold the pipe
	// open past Process.Kill and stall Wait for the child's lifetime
	// (TestErrTimeout hung 60s on `sh -c 'sleep 60'`). WaitDelay force-closes
	// the pipes shortly after the process dies.
	cmd.WaitDelay = 500 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: spawn %s: %v", ErrNoServer, bin, err)
	}

	c := &Client{
		lang:       lang,
		rootDir:    abs,
		cmd:        cmd,
		stdin:      stdin,
		out:        bufio.NewReader(stdout),
		pending:    make(map[int64]chan rpcResult),
		readerDone: make(chan struct{}),
	}
	go c.readLoop()

	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	if _, err := c.request(hctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootURI":   uriOf(abs),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
		},
	}); err != nil {
		c.forceStop()
		return nil, err
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.forceStop()
		return nil, err
	}
	c.touch()
	return c, nil
}

// Language returns the language the server was started for.
func (c *Client) Language() langs.Language { return c.lang }

// Root returns the absolute workspace root the server was started in.
func (c *Client) Root() string { return c.rootDir }

// IdleFor reports how long the client has gone without a request.
func (c *Client) IdleFor() time.Duration {
	c.lastUseMu.Lock()
	defer c.lastUseMu.Unlock()
	return time.Since(c.lastUse)
}

// Close performs a graceful shutdown: a shutdown request (bounded by 2s), an
// exit notification, then process teardown. It is idempotent and best-effort;
// it always returns nil.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = c.request(ctx, "shutdown", nil)
		_ = c.notify("exit", nil)
		c.forceStop()
	})
	return nil
}

// forceStop tears the process down without the graceful handshake and waits
// for the reader goroutine so no zombie or goroutine is left behind.
func (c *Client) forceStop() {
	c.stdinMu.Lock()
	_ = c.stdin.Close()
	c.stdinMu.Unlock()

	waited := make(chan struct{})
	go func() {
		killer := time.AfterFunc(2*time.Second, func() { _ = c.cmd.Process.Kill() })
		_ = c.cmd.Wait()
		killer.Stop()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(4 * time.Second):
		_ = c.cmd.Process.Kill()
		<-waited
	}
	<-c.readerDone
}

func (c *Client) touch() {
	c.lastUseMu.Lock()
	c.lastUse = time.Now()
	c.lastUseMu.Unlock()
}

// DocumentSymbols opens absPath with textDocument/didOpen and returns its
// normalized document symbols (hierarchical children flattened).
func (c *Client) DocumentSymbols(ctx context.Context, absPath string) ([]Symbol, error) {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return nil, err
	}
	text, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	uri := uriOf(abs)
	if err := c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": string(c.lang),
			"version":    1,
			"text":       string(text),
		},
	}); err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		return nil, err
	}
	return normalizeSymbols(raw)
}

// WorkspaceSymbols runs workspace/symbol and returns normalized results.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	raw, err := c.request(ctx, "workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	return normalizeSymbols(raw)
}

// request sends a JSON-RPC request and waits for the correlated response,
// honoring ctx. An expired ctx yields an error matching ErrTimeout.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.touch()
	c.mu.Lock()
	if c.broken != nil {
		err := c.broken
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.dropPending(id)
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.dropPending(id)
		return nil, fmt.Errorf("%w: %s: %v", ErrTimeout, method, ctx.Err())
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *Client) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) send(msg map[string]any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	method, _ := msg["method"].(string)
	c.stdinMu.Lock()
	defer c.stdinMu.Unlock()
	if err := writeFrame(c.stdin, payload); err != nil {
		return fmt.Errorf("%w: write %s: %v", ErrNoServer, method, err)
	}
	return nil
}

func (c *Client) dropPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// readLoop dispatches responses to pending requests. Once the stream dies it
// fails every outstanding request and stores a sticky error so later calls
// fail fast instead of blocking until their own deadline.
func (c *Client) readLoop() {
	defer close(c.readerDone)
	for {
		payload, err := readFrame(c.out)
		if err != nil {
			c.failAll(fmt.Errorf("%w: server stream closed: %v", ErrNoServer, err))
			return
		}
		var m struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(payload, &m); err != nil || m.ID == nil {
			continue // server-initiated notification, or message we cannot route
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch == nil {
			continue // response to a request whose caller already gave up
		}
		if m.Error != nil {
			ch <- rpcResult{err: fmt.Errorf("%w: %s (code %d)", ErrProtocol, m.Error.Message, m.Error.Code)}
		} else {
			ch <- rpcResult{result: m.Result}
		}
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	c.broken = err
	pending := c.pending
	c.pending = make(map[int64]chan rpcResult)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: err}
	}
}

// uriOf converts an absolute path to a file:// URI.
//
// ponytail: no percent-encoding, so paths with spaces or non-ASCII segments
// produce URIs strict servers may reject. Upgrade path:
// (&url.URL{Scheme: "file", Path: absPath}).String().
func uriOf(absPath string) string {
	return "file://" + filepath.ToSlash(absPath)
}

type rawPos struct {
	Line int `json:"line"`
}

type rawRange struct {
	Start rawPos `json:"start"`
	End   rawPos `json:"end"`
}

// rawSym covers both the DocumentSymbol shape (Range, Children) and the
// SymbolInformation shape (Location.Range) so a single normalizer handles
// either result.
type rawSym struct {
	Name     string    `json:"name"`
	Kind     int       `json:"kind"`
	Range    *rawRange `json:"range"`
	Location *struct {
		Range rawRange `json:"range"`
	} `json:"location"`
	Children []rawSym `json:"children"`
}

func normalizeSymbols(raw json.RawMessage) ([]Symbol, error) {
	var roots []rawSym
	if err := json.Unmarshal(raw, &roots); err != nil {
		return nil, fmt.Errorf("%w: decode symbols: %v", ErrProtocol, err)
	}
	var out []Symbol
	var walk func([]rawSym)
	walk = func(ss []rawSym) {
		for _, s := range ss {
			r := s.Range
			if r == nil && s.Location != nil {
				r = &s.Location.Range
			}
			if r != nil {
				out = append(out, Symbol{
					Name:      s.Name,
					Kind:      s.Kind,
					StartLine: r.Start.Line + 1, // LSP lines are 0-based; we store 1-based
					EndLine:   r.End.Line + 1,
				})
			}
			walk(s.Children)
		}
	}
	walk(roots)
	return out, nil
}
