package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
)

func decodeRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
}

func TestDecodeJSONAcceptsABodyUnderTheLimit(t *testing.T) {
	var v struct {
		Query string `json:"query"`
	}
	rec := httptest.NewRecorder()
	if err := DecodeJSON(rec, decodeRequest(`{"query":"Alpha"}`), &v); err != nil || v.Query != "Alpha" {
		t.Fatalf("DecodeJSON = %v, query %q; want a clean decode", err, v.Query)
	}
}

func TestDecodeJSONRefusesABodyOverOneMiBWith413(t *testing.T) {
	big := `{"query":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	var v map[string]any
	rec := httptest.NewRecorder()
	err := DecodeJSON(rec, decodeRequest(big), &v)
	if CodeOf(err) != errs.RequestTooLarge.Code {
		t.Fatalf("DecodeJSON(>1 MiB) = %v; want code %s", err, errs.RequestTooLarge.Code)
	}
	if got := StatusFor(err); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("StatusFor = %d, want 413", got)
	}
}

func TestDecodeJSONKeepsMalformedBodiesAsInvalidParam(t *testing.T) {
	var v map[string]any
	err := DecodeJSON(httptest.NewRecorder(), decodeRequest(`{"query":`), &v)
	var coded *errs.Error
	if !errors.As(err, &coded) || coded.Code() != errs.InvalidParam.Code {
		t.Fatalf("DecodeJSON(malformed) = %v; want code %s", err, errs.InvalidParam.Code)
	}
}
