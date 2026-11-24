package embed

import (
	"strings"
	"testing"
)

// fakeSecretContent is a function whose body carries an AWS-style key, a
// bearer token and a PEM private key header. The secrets are fake.
const fakeSecretContent = `func load() string {
	key := "AKIAEXAMPLEFAKE12345"
	req.Header.Set("Authorization", "Bearer abc123def456ghi789")
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEFAKEBODY0123\n-----END RSA PRIVATE KEY-----"
	return key
}`

var fakeSecrets = []string{"AKIAEXAMPLEFAKE12345", "abc123def456ghi789", "BEGIN RSA PRIVATE KEY", "MIIEFAKEBODY0123"}

func TestDocumentInputRedactsSecretsKeepsCode(t *testing.T) {
	p := Deterministic(8)
	got := documentInput(p, 100000, elementText{File: "a.go", QN: "a::load", Type: "function", Name: "load", Content: fakeSecretContent})

	for _, s := range fakeSecrets {
		if strings.Contains(got, s) {
			t.Errorf("documentInput still carries secret %q:\n%s", s, got)
		}
	}
	for _, code := range []string{"func load() string {", "req.Header.Set(", "return key"} {
		if !strings.Contains(got, code) {
			t.Errorf("documentInput lost code %q:\n%s", code, got)
		}
	}
}

func TestDocumentInputRedactionIsDeterministic(t *testing.T) {
	p := Deterministic(8)
	e := elementText{File: "a.go", QN: "a::load", Type: "function", Name: "load", Content: fakeSecretContent}
	first := documentInput(p, 100000, e)
	for i := 0; i < 3; i++ {
		if again := documentInput(p, 100000, e); again != first {
			t.Fatalf("documentInput changed between calls:\nfirst %q\nagain %q", first, again)
		}
	}
}

// TestRunSendsNoSecretToProvider: a live run hands the provider redacted text
// only, so no secret reaches the embedding endpoint.
func TestRunSendsNoSecretToProvider(t *testing.T) {
	st := testStore(t)
	seed(t, st, el("a::load", fakeSecretContent))
	rec := &recordProvider{inner: Deterministic(8)}

	mustRun(t, st, rec, "full")

	if len(rec.texts) != 1 {
		t.Fatalf("provider got %d document texts, want 1", len(rec.texts))
	}
	for _, s := range fakeSecrets {
		if strings.Contains(rec.texts[0], s) {
			t.Errorf("provider received secret %q in %q", s, rec.texts[0])
		}
	}
	if !strings.Contains(rec.texts[0], "return key") {
		t.Errorf("provider text lost surrounding code: %q", rec.texts[0])
	}
}
