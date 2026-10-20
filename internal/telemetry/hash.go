package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// ArgsHash is the sha256 hex of the canonical form of a JSON args object:
// keys sorted at every level, no insignificant whitespace. The capture
// middleware and every sessionlink adapter hash with this one function so a
// captured call and its transcript tool_use entry compare equal. Invalid
// JSON hashes its raw bytes.
func ArgsHash(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	canon, _ := json.Marshal(canonical(v)) // encoding/json sorts map keys
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

func canonical(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = canonical(x)
		}
		return out
	case []any:
		for i := range t {
			t[i] = canonical(t[i])
		}
		return t
	default:
		return v
	}
}

// ArgKeys returns the sorted, comma-joined top-level keys of a JSON object.
func ArgKeys(raw []byte) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
