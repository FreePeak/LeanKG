package telemetry

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Fixture secrets are synthetic. They match each shape without being live keys.
func TestRedactSecretShapes(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		notContain []string
		exact      string // when set, the output must equal this
	}{
		{"bearer token", "Authorization: Bearer abcdef0123456789XYZ", []string{"abcdef0123456789XYZ"}, ""},
		{"jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", []string{"eyJhbGciOiJIUzI1NiJ9"}, ""},
		{"openai project key", "key sk-proj-abcdefghijklmnopqrstuv1234", []string{"abcdefghijklmnopqrstuv1234"}, ""},
		{"anthropic key", "x sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA y", []string{"AAAAAAAAAAAAAAAAAAAAAAAA"}, ""},
		{"github classic pat", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", []string{"abcdefghijklmnopqrstuvwxyz0123456789"}, ""},
		{"github fine-grained pat", "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz", []string{"11ABCDEFG0123456789"}, ""},
		{"aws access key", "AKIAIOSFODNN7EXAMPLE", []string{"AKIAIOSFODNN7EXAMPLE"}, ""},
		{"slack token", "xoxb-1234567890-abcdefghij", []string{"1234567890-abcdefghij"}, ""},
		{"password kv", "db password=hunter2supersecret&x=1", []string{"hunter2supersecret"}, ""},
		{"json token", `{"token": "zzSECRETvalue123"}`, []string{"zzSECRETvalue123"}, ""},
		{"env secret", "API_SECRET=topsecretvalue", []string{"topsecretvalue"}, ""},
		{"pem private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEabcdef\n-----END RSA PRIVATE KEY-----", []string{"MIIEabcdef"}, ""},
		{"unterminated pem", "-----BEGIN PRIVATE KEY-----\nMIIEtailbytes", []string{"MIIEtailbytes"}, ""},
		{"benign max_tokens kept", "max_tokens=5 and query=foo", nil, "max_tokens=5 and query=foo"},
		{"benign prose kept", "plain text without secrets", nil, "plain text without secrets"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			if c.exact != "" && got != c.exact {
				t.Fatalf("Redact(%q) = %q, want %q", c.in, got, c.exact)
			}
			for _, s := range c.notContain {
				if strings.Contains(got, s) {
					t.Fatalf("Redact(%q) = %q still contains %q", c.in, got, s)
				}
			}
		})
	}
}

func TestRedactRewritesHomePaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fakehome")
	t.Setenv("HOME", home)
	in := "open " + home + "/proj/main.go and " + home
	got := Redact(in)
	want := "open ~/proj/main.go and ~"
	if got != want {
		t.Fatalf("Redact(%q) = %q, want %q", in, got, want)
	}
	// A longer sibling directory must not be rewritten as if it were home.
	sib := home + "2/x"
	if got := Redact(sib); got != sib {
		t.Fatalf("sibling path rewritten: %q", got)
	}
}

func TestCapTruncatesOnRuneBoundary(t *testing.T) {
	if got := Cap("abcdef", 3); got != "abc...[truncated]" {
		t.Fatalf("Cap ascii = %q", got)
	}
	if got := Cap("short", 100); got != "short" {
		t.Fatalf("Cap under limit = %q", got)
	}
	if got := Cap("abcdef", 0); got != "abcdef" {
		t.Fatalf("Cap max 0 must mean no cap, got %q", got)
	}
	out := Cap("h\u00e9llo", 2) // the second byte of the 2-byte rune sits at the cut
	if !utf8.ValidString(out) {
		t.Fatalf("Cap produced invalid UTF-8: %q", out)
	}
}

// ExpandHome is the inverse of the "~" rewrite Redact applies to paths, so a
// redacted transcript path can be opened again (smoke-found).
func TestExpandHomeInvertsRedactHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".claude", "projects", "x", "s.jsonl")
	if got := ExpandHome(Redact(p)); got != p {
		t.Fatalf("ExpandHome(Redact(%q)) = %q", p, got)
	}
	for _, keep := range []string{"/abs/path", "rel/path", "", "~user/x"} {
		if got := ExpandHome(keep); got != keep {
			t.Fatalf("ExpandHome(%q) = %q, want unchanged", keep, got)
		}
	}
	if got := ExpandHome("~"); got != home {
		t.Fatalf("ExpandHome(~) = %q, want %q", got, home)
	}
}
