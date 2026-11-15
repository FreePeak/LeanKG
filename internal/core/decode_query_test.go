package core

import (
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// TestDecodeQueryFields is the table over the union of fields the four
// transports carry: the JSON object (REST, MCP), the proto message (RPC, whose
// limit is int32 and whose args are strings), and the flat web params.
func TestDecodeQueryFields(t *testing.T) {
	cases := []struct {
		name     string
		in       map[string]any
		want     QueryRequest
		wantCode string
	}{
		{"absent fields are zero", nil, QueryRequest{}, ""},
		{"json numbers and strings", map[string]any{"action": "exact", "query": "x", "limit": float64(5)},
			QueryRequest{Action: "exact", Query: "x", Limit: 5}, ""},
		{"args object", map[string]any{"args": map[string]any{"path": "a.go"}},
			QueryRequest{Args: map[string]any{"path": "a.go"}}, ""},
		{"proto int32 limit and string args", map[string]any{"limit": int32(3), "args": map[string]string{"scope": "code"}},
			QueryRequest{Limit: 3, Args: map[string]any{"scope": "code"}}, ""},
		{"unknown keys are ignored", map[string]any{"query": "x", "extra": true},
			QueryRequest{Query: "x"}, ""},
		{"keys match case-insensitively like encoding/json", map[string]any{"Action": "exact", "QUERY": "x"},
			QueryRequest{Action: "exact", Query: "x"}, ""},
		{"fractional limit", map[string]any{"limit": 2.5}, QueryRequest{}, errs.InvalidParam.Code},
		{"string limit", map[string]any{"limit": "5"}, QueryRequest{}, errs.InvalidParam.Code},
		{"non-string action", map[string]any{"action": float64(1)}, QueryRequest{}, errs.InvalidParam.Code},
		{"args not an object", map[string]any{"args": "path"}, QueryRequest{}, errs.InvalidParam.Code},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeQuery(c.in)
			if c.wantCode != "" {
				coded, ok := err.(*errs.Error)
				if !ok || coded.Code() != c.wantCode {
					t.Fatalf("err = %v, want code %s", err, c.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestDecodeQueryParamsSplitsTheWebForm checks that the flat web params
// (action and limit reserved, every other key an argument) decode to the same
// request as the nested form.
func TestDecodeQueryParamsSplitsTheWebForm(t *testing.T) {
	flat, err := DecodeQueryParams("x", map[string]any{"action": "exact", "limit": float64(4), "path": "a.go"})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := DecodeQuery(map[string]any{"query": "x", "action": "exact", "limit": float64(4),
		"args": map[string]any{"path": "a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(flat, nested) {
		t.Fatalf("flat %+v != nested %+v", flat, nested)
	}
}
