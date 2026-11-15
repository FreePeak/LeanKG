package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
)

func TestStatusForIsTheOneErrorTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unauthorized", errs.NewError(errs.Unauthorized, "no token", ""), http.StatusUnauthorized},
		{"permission denied", errs.NewError(errs.PermissionDenied, "reader role", ""), http.StatusForbidden},
		{"path outside project stays a bad request (REST answered 400 before the table)", errs.NewError(errs.PathOutsideProject, "climbs out", ""), http.StatusBadRequest},
		{"wrapped coded error", fmt.Errorf("query: %w", errs.NewError(errs.PermissionDenied, "x", "")), http.StatusForbidden},
		{"unknown action is a bad request", errs.NewError(errs.UnknownAction, "nope", ""), http.StatusBadRequest},
		{"invalid parameter is a bad request", errs.NewError(errs.InvalidParam, "limit", ""), http.StatusBadRequest},
		{"uncoded error is a bad request", errors.New("boom"), http.StatusBadRequest},
		{"cancelled call", context.Canceled, StatusClientClosedRequest},
		{"wrapped cancelled call", fmt.Errorf("walk: %w", context.Canceled), StatusClientClosedRequest},
	}
	for _, c := range cases {
		if got := StatusFor(c.err); got != c.want {
			t.Errorf("%s: StatusFor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestWriteErrorRendersOneBodyShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errs.NewError(errs.PermissionDenied, "the reader role", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["code"] != errs.PermissionDenied.Code {
		t.Fatalf("code = %v, want %s", body["code"], errs.PermissionDenied.Code)
	}
	if msg, _ := body["error"].(string); !strings.HasPrefix(msg, errs.PermissionDenied.Code+": ") {
		t.Fatalf("error = %q, want the rendered catalog message", msg)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestWriteErrorUncodedHasNoCodeField(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errors.New("boom"))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["code"]; ok || body["error"] != "boom" || rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %v; want 400 {error: boom} with no code", rec.Code, body)
	}
}

func TestWriteErrorCancelledWritesStatusOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, context.Canceled)
	if rec.Code != StatusClientClosedRequest || rec.Body.Len() != 0 {
		t.Fatalf("status %d body %q; a cancelled call answers 499 with no body", rec.Code, rec.Body.String())
	}
}

// TestNoDuplicateErrorWriters fails when a transport declares its own JSON or
// error writer again. The one copy lives in this package; every other
// transport calls WriteJSON, WriteError or WriteMessage.
func TestNoDuplicateErrorWriters(t *testing.T) {
	banned := regexp.MustCompile(`(?m)^func (writeJSON|writeErr|writeError|writeEnvelope|writeEnvelopeStatus|writeAutoJSON|writeProjectError|httpError)\(`)
	root := filepath.Join("..", "..")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".claude", "node_modules", "testdata", "vendor", "ui-v2":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "internal/httpx/") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, m := range banned.FindAllString(string(src), -1) {
			hits = append(hits, fmt.Sprintf("%s: %s", path, strings.TrimSpace(m)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("duplicate HTTP writers outside internal/httpx:\n%s", strings.Join(hits, "\n"))
	}
}
