# internal/astgrep contract (CLI wrapper)

ast-grep is an EXTERNAL binary (probed via langs.Tiers). This package wraps it:
  type Runner struct{ Bin string }             // Bin defaults to "ast-grep", then "sg"
  func New() (*Runner, error)                  // ErrNotInstalled when absent
  func (r *Runner) RunPattern(ctx context.Context, lang, pattern, rootDir string, max int) ([]Match, error)
      // exec: <bin> run --pattern <pat> --lang <lang> <rootDir> --json
      // Match{File string, Line, Col int, Text string}; parse --json output
  func (r *Runner) Version(ctx context.Context) (string, error)
Rules: context-bounded exec; 10s default timeout; never shell-inject (args as
argv, never a shell string); typed errors (ErrNotInstalled, ErrTimeout).
