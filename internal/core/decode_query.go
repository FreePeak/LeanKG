package core

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// DecodeQuery reads one query request from the fields a transport carries:
// action and query (strings), limit (a whole number: a JSON number or a Go
// integer from a typed transport) and args (an object of any values, or of
// strings from the proto wire). Absent keys take their zero value and unknown
// keys are ignored. A wrong type or a fractional limit is an
// errs.InvalidParam error, whichever transport the request came in on.
func DecodeQuery(fields map[string]any) (QueryRequest, error) {
	var q QueryRequest
	var err error
	if q.Action, err = stringField(fields, "action"); err != nil {
		return QueryRequest{}, err
	}
	if q.Query, err = stringField(fields, "query"); err != nil {
		return QueryRequest{}, err
	}
	if q.Limit, err = limitField(fields, "limit"); err != nil {
		return QueryRequest{}, err
	}
	if q.Args, err = argsField(fields, "args"); err != nil {
		return QueryRequest{}, err
	}
	return q, nil
}

// DecodeQueryParams reads the web API's flat form, {query, params}: params
// names action and limit, and every other key is an action argument.
func DecodeQueryParams(query string, params map[string]any) (QueryRequest, error) {
	args := map[string]any{}
	for k, v := range params {
		if !isFlatReserved(k) {
			args[k] = v
		}
	}
	if len(args) == 0 {
		args = nil
	}
	fields := map[string]any{"query": query, "args": args}
	for _, key := range []string{"action", "limit"} {
		if v, ok := lookup(params, key); ok {
			fields[key] = v
		}
	}
	return DecodeQuery(fields)
}

func isFlatReserved(key string) bool {
	return strings.EqualFold(key, "action") || strings.EqualFold(key, "limit")
}

// lookup returns the value of key: an exact match first, else the first key
// that matches it case-insensitively in sorted order, as encoding/json does
// for struct fields.
func lookup(fields map[string]any, key string) (any, bool) {
	if v, ok := fields[key]; ok {
		return v, true
	}
	var matches []string
	for k := range fields {
		if strings.EqualFold(k, key) {
			matches = append(matches, k)
		}
	}
	if len(matches) == 0 {
		return nil, false
	}
	sort.Strings(matches)
	return fields[matches[0]], true
}

func invalidParam(key, want string) error {
	return errs.NewError(errs.InvalidParam, fmt.Sprintf("%s must be %s", key, want), "")
}

func stringField(fields map[string]any, key string) (string, error) {
	v, ok := lookup(fields, key)
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", invalidParam(key, "a string")
	}
	return s, nil
}

func limitField(fields map[string]any, key string) (int, error) {
	v, ok := lookup(fields, key)
	if !ok || v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case float64:
		if n != math.Trunc(n) || n < math.MinInt || n > math.MaxInt {
			return 0, invalidParam(key, "a whole number")
		}
		return int(n), nil
	}
	return 0, invalidParam(key, "a whole number")
}

func argsField(fields map[string]any, key string) (map[string]any, error) {
	v, ok := lookup(fields, key)
	if !ok || v == nil {
		return nil, nil
	}
	switch a := v.(type) {
	case map[string]any:
		return a, nil
	case map[string]string:
		out := make(map[string]any, len(a))
		for k, s := range a {
			out[k] = s
		}
		return out, nil
	}
	return nil, invalidParam(key, "an object")
}
