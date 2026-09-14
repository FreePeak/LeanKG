# internal/lsp contract (lazy LSP client)

Read `internal/langs/langs.go` first: Profile.LSP = {Commands, RootMarkers};
the Registry resolves availability. This client SPAWNS nothing until Query time.

Public API (package lsp):
  type Client struct{ ... }                    // one per (language, rootDir)
  func Available(spec *langs.LSPSpec) bool     // any command resolvable on PATH
  func Start(ctx context.Context, lang langs.Language, spec *langs.LSPSpec, rootDir string) (*Client, error)
      // spawns the first resolvable command, LSP initialize handshake with
      // rootURI=file://rootDir, capabilities negotiation (no blocking waits >5s)
  func (c *Client) DocumentSymbols(ctx context.Context, absPath string) ([]Symbol, error)
      // textDocument/didOpen + textDocument/documentSymbol; Symbol{Name,Kind,Range{StartLine,EndLine}}
  func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error)
      // workspace/symbol with the query string
  func (c *Client) IdleFor() time.Duration     // since last use
  func (c *Client) Close() error               // shutdown + exit + kill

  type Manager struct{ ... }
  func NewManager(ttl time.Duration) *Manager
  func (m *Manager) Get(ctx context.Context, lang langs.Language, spec *langs.LSPSpec, rootDir string) (*Client, error)
      // returns an existing live client for (lang, rootDir) or starts one;
      // evicts+closes clients idle beyond ttl (lazy lifecycle)
  func (m *Manager) Shutdown()                 // closes everything

Rules:
- stdio JSON-RPC 2.0 with Content-Length framing (implement the framing;
  do NOT pull an LSP dependency — go.mod is frozen).
- Never block >5s on a server that won't start: kill and return an error.
- Pool one process per (language, rootDir); reuse across calls.
- All failures surface as typed errors (ErrNoServer, ErrTimeout, ErrProtocol).
