package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testAdminPassword = "correct horse battery staple"

// newAdminServer builds a server over a fresh store that holds one site-admin
// account (username / testAdminPassword), wired the way serve wires its HTTP
// token store.
func newAdminServer(t *testing.T, username string) (*Server, store.Backend) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateSiteAdmin(st, username, testAdminPassword); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(dir)
	srv := New(engine)
	srv.SetAuthStore(st)
	return srv, st
}

// callAuthenticate runs the authenticate tool over the in-memory transport. A
// refused login comes back as the call's error, as the SDK reports handler
// errors.
func callAuthenticate(session *mcp.ClientSession, username, password string) (*mcp.CallToolResult, error) {
	return session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "authenticate",
		Arguments: map[string]any{"username": username, "password": password},
	})
}

func toolText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("tool result content is %T, want text", res.Content[0])
	}
	return text.Text
}

// TestAuthenticateMintsAdminToken pins slice 1: a good login returns a bearer
// token with role admin and a 24h expiry, and that token resolves to Admin
// through the existing token store (the same path `leankg auth token create`
// uses).
func TestAuthenticateMintsAdminToken(t *testing.T) {
	srv, st := newAdminServer(t, "owner")
	session := connectTo(t, srv)

	before := time.Now()
	res, err := callAuthenticate(session, "owner", testAdminPassword)
	if err != nil {
		t.Fatalf("good login failed: %v", err)
	}
	var out struct {
		Token     string `json:"token"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(toolText(t, res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if !strings.HasPrefix(out.Token, "lkg_") {
		t.Fatalf("token has no lkg_ prefix")
	}
	if out.Role != "admin" {
		t.Fatalf("role = %q, want admin", out.Role)
	}
	expires, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", out.ExpiresAt, err)
	}
	if d := expires.Sub(before); d < 24*time.Hour-time.Minute || d > 24*time.Hour+time.Minute {
		t.Fatalf("expires_at is %v after login, want about 24h", d)
	}

	grant, ok, err := auth.Verify(st, out.Token)
	if err != nil || !ok {
		t.Fatalf("minted token does not verify: ok=%v err=%v", ok, err)
	}
	if grant.Role != auth.Admin {
		t.Fatalf("minted token role = %v, want Admin", grant.Role)
	}
}

// TestAuthenticateFailuresAreGenericThenRateLimited pins slice 2: a wrong
// password and an unknown username return the same generic error, and after
// five consecutive failures for one username the tool names the rate limit
// (even a correct password is refused during the lockout).
func TestAuthenticateFailuresAreGenericThenRateLimited(t *testing.T) {
	username := "throttle-admin"
	srv, _ := newAdminServer(t, username)
	session := connectTo(t, srv)

	wrongErr := refusal(t, session, username, "not the password")
	unknownErr := refusal(t, session, "nobody-here", "not the password")
	for name, msg := range map[string]string{"wrong password": wrongErr, "unknown user": unknownErr} {
		if !strings.Contains(msg, "invalid credentials") || strings.Contains(msg, "too many") {
			t.Fatalf("%s: want the generic invalid-credentials error, got %q", name, msg)
		}
	}

	// Four more failures complete the streak of five for this username.
	for i := 0; i < 4; i++ {
		refusal(t, session, username, "not the password")
	}
	locked := refusal(t, session, username, testAdminPassword)
	if !strings.Contains(locked, "too many failed sign-in attempts") {
		t.Fatalf("lockout must name the rate limit, got %q", locked)
	}
}

// refusal runs a login that must fail and returns the error text.
func refusal(t *testing.T, session *mcp.ClientSession, username, password string) string {
	t.Helper()
	res, err := callAuthenticate(session, username, password)
	if err == nil {
		t.Fatalf("login for %q was accepted: %s", username, toolText(t, res))
	}
	return err.Error()
}

// TestAuthenticateSchemaIsServerLevel pins that login takes no project. On a
// multi-project server every other tool requires one, but authenticate
// advertises no project property and refuses extra properties over the wire.
func TestAuthenticateSchemaIsServerLevel(t *testing.T) {
	_, session := newProjectServerForTest(t, WithProjectRequired(true))
	for _, tool := range listSchemas(t, session) {
		if tool.name != core.ToolAuthenticate {
			continue
		}
		if tool.projectDesc != "" || containsStr(tool.required, "project") {
			t.Fatalf("authenticate must not carry a project argument: required=%v desc=%q", tool.required, tool.projectDesc)
		}
		if !containsStr(tool.required, "username") || !containsStr(tool.required, "password") {
			t.Fatalf("authenticate must require username and password: %v", tool.required)
		}
	}
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "authenticate",
		Arguments: map[string]any{"username": "x", "password": "y", "project": "/some/repo"},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatal("authenticate accepted an extra project argument; the schema must refuse it")
	}
}

// TestAuthenticateNeverRecordsSecrets pins the redaction rule: at the Bodies
// capture level, no recorded event for authenticate carries the password (in
// args, hash, keys or body) or the minted token. The failure path is checked
// too, with a wrong password.
func TestAuthenticateNeverRecordsSecrets(t *testing.T) {
	srv, _ := newAdminServer(t, "capture-admin")
	rec := &fakeRec{level: telemetry.Bodies}
	srv.SetRecorder(rec)
	session := connectTo(t, srv)

	res, err := callAuthenticate(session, "capture-admin", testAdminPassword)
	if err != nil {
		t.Fatalf("good login failed: %v", err)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(toolText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	const wrong = "wrong-password-sentinel-91"
	if _, err := callAuthenticate(session, "capture-admin", wrong); err == nil {
		t.Fatal("wrong password was accepted")
	}

	var events []telemetry.CallEvent
	for _, ev := range rec.byMethod("tools/call") {
		if ev.Tool == core.ToolAuthenticate {
			events = append(events, ev)
		}
	}
	if len(events) != 2 {
		t.Fatalf("recorded %d authenticate events, want 2", len(events))
	}
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{testAdminPassword, wrong, out.Token} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("recorded authenticate event leaks a secret: %s", raw)
			}
		}
		if ev.ArgsHash != "" || ev.ArgsRedacted != "" || ev.ArgKeys != "" {
			t.Fatalf("authenticate args must not be hashed or recorded: hash=%q keys=%q args=%q",
				ev.ArgsHash, ev.ArgKeys, ev.ArgsRedacted)
		}
	}
}

// TestAuthenticateWorksOverStdioWithoutAuthStore pins that the stdio session,
// which never gets SetAuthStore, can still log in: the login is checked and the
// token minted in the engine's own store.
func TestAuthenticateWorksOverStdioWithoutAuthStore(t *testing.T) {
	st := newGateStore(t)
	if _, err := auth.CreateSiteAdmin(st, "stdio-login", testAdminPassword); err != nil {
		t.Fatal(err)
	}
	engine := core.New(st, nil, nil)
	engine.SetProjectDir(t.TempDir())
	session := connectTo(t, New(engine))

	res, err := callAuthenticate(session, "stdio-login", testAdminPassword)
	if err != nil {
		t.Fatalf("stdio login failed: %v", err)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(toolText(t, res)), &out); err != nil || !strings.HasPrefix(out.Token, "lkg_") {
		t.Fatalf("stdio login returned no token: %v %s", err, toolText(t, res))
	}
	if _, ok, err := auth.Verify(st, out.Token); err != nil || !ok {
		t.Fatalf("stdio-minted token is not in the engine store: ok=%v err=%v", ok, err)
	}
}
