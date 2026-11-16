package core

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/internal/errs"
)

// The typed action args (CX-44). Each action's args are decoded once, at the
// registry seam, into the struct that names the keys the action reads. The
// handler never digs into the map. A key the struct does not name, or a value
// of the wrong type, is an INVALID_PARAM error that names the key, so a typo
// fails loudly instead of being ignored.
//
// The proto wire carries every arg as a string, so an int or bool field also
// accepts its string spelling, and a []string field also accepts a
// comma-separated string (the shape an agent sends against one advertised
// field). A nil value is the same as an absent key, and so is "" for a number
// or a boolean (the proto wire's unset value).

// decodeArgs decodes args into T and refuses a key T does not name.
func decodeArgs[T any](args map[string]any) (T, error) {
	return decodeArgsWith[T](args, true)
}

// decodeKnownArgs decodes only the keys T names and leaves the rest to the
// action. Query uses it for the envelope, which it validates before dispatch.
func decodeKnownArgs[T any](args map[string]any) (T, error) {
	return decodeArgsWith[T](args, false)
}

// noArgs is the args struct of an action that takes none.
type noArgs struct{}

// envelopeArgs are the args every query action accepts. Query validates scope
// and weight before the ladder runs, and budget.go reads token_budget for every
// transport, so each query action's struct embeds this one.
type envelopeArgs struct {
	Scope       string `json:"scope"`
	Weight      string `json:"weight"`
	IncludeGit  bool   `json:"include_git"`
	TokenBudget any    `json:"token_budget"`
}

func decodeArgsWith[T any](args map[string]any, strict bool) (T, error) {
	var out T
	fields := map[string]reflect.Value{}
	collectArgFields(reflect.ValueOf(&out).Elem(), fields)
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, ok := fields[k]
		if !ok {
			if strict {
				return out, unknownArgError(k, fields)
			}
			continue
		}
		if args[k] == nil {
			continue
		}
		if err := setArg(f, k, args[k]); err != nil {
			var zero T
			return zero, err
		}
	}
	return out, nil
}

// collectArgFields maps each json name of v's struct fields (embedded structs
// flattened) to the field's value.
func collectArgFields(v reflect.Value, into map[string]reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			collectArgFields(fv, into)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		name := strings.Split(sf.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		into[name] = fv
	}
}

func unknownArgError(key string, fields map[string]reflect.Value) error {
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return errs.NewError(errs.InvalidParam, fmt.Sprintf("unknown args key %q (this action takes no args)", key), "")
	}
	return errs.NewError(errs.InvalidParam, fmt.Sprintf("unknown args key %q (valid: %s)", key, strings.Join(names, ", ")), "")
}

func argTypeError(key, want string) error {
	return errs.NewError(errs.InvalidParam, fmt.Sprintf("args.%s must be %s", key, want), "")
}

// setArg stores raw into f, which is addressable. key names the arg in errors.
func setArg(f reflect.Value, key string, raw any) error {
	if s, ok := raw.(string); ok && s == "" && isNumOrBool(f.Type()) {
		return nil
	}
	switch f.Kind() {
	case reflect.String:
		s, ok := raw.(string)
		if !ok {
			return argTypeError(key, "a string")
		}
		f.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := wholeArg(raw)
		if !ok || f.OverflowInt(n) {
			return argTypeError(key, "a whole number")
		}
		f.SetInt(n)
	case reflect.Bool:
		switch t := raw.(type) {
		case bool:
			f.SetBool(t)
		case string:
			b, err := strconv.ParseBool(t)
			if err != nil {
				return argTypeError(key, "a boolean")
			}
			f.SetBool(b)
		default:
			return argTypeError(key, "a boolean")
		}
	case reflect.Ptr:
		p := reflect.New(f.Type().Elem())
		if err := setArg(p.Elem(), key, raw); err != nil {
			return err
		}
		f.Set(p)
	case reflect.Slice:
		list, ok := stringList(raw)
		if !ok {
			return argTypeError(key, "a list of strings")
		}
		f.Set(reflect.ValueOf(list))
	case reflect.Map:
		m, ok := raw.(map[string]any)
		if !ok {
			return argTypeError(key, "an object")
		}
		f.Set(reflect.ValueOf(m))
	case reflect.Interface:
		f.Set(reflect.ValueOf(raw))
	default:
		return fmt.Errorf("args: %s has unsupported kind %s", key, f.Kind())
	}
	return nil
}

// isNumOrBool reports whether t is an integer or a boolean, or a pointer to one.
func isNumOrBool(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	}
	return false
}

// wholeArg reads a whole number from a JSON number, a Go integer or a decimal
// string.
func wholeArg(raw any) (int64, bool) {
	switch t := raw.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		if t != math.Trunc(t) || math.Abs(t) > math.MaxInt64/2 {
			return 0, false
		}
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// stringList reads a list of strings from a JSON array, a []string or a
// comma-separated string.
func stringList(raw any) ([]string, bool) {
	switch t := raw.(type) {
	case string:
		return splitCommaList(t), true
	case []string:
		return append([]string(nil), t...), true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
