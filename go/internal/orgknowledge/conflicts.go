// Environment-conflict detection: the Rust `leankg env-conflicts --service`
// verb, the find_env_conflicts MCP tool and /api/v2/env/diff, which are all
// GraphEngine::find_env_conflicts (src/graph/query.rs).
//
// Input: the env-scoped element view. The Rust engine kept one code_elements
// row per (qualified_name, env); the Go engine keys code_elements on
// qualified_name alone, so the same input is carried by the env_snapshots
// table and written through SnapshotElements. Detection itself is unchanged:
// per-env presence, then metadata comparison between the envs that exist.
package orgknowledge

import (
	"encoding/json"
	"fmt"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// StandardEnvs is the environment set the Rust check walked. The Rust code
// iterated a HashMap, so which present env served as the comparison base was
// random; this port walks local -> staging -> production, which makes the
// detail strings deterministic without changing the conflict set.
var StandardEnvs = []string{"local", "staging", "production"}

// EnvConflict is one finding (Rust graph::query::EnvConflict).
type EnvConflict struct {
	ConflictType string `json:"conflict_type"`
	Detail       string `json:"detail"`
	Risk         string `json:"risk"`
}

// SnapshotElements records the current state of elements as one environment's
// view, replacing any earlier snapshot for the same (env, qualified_name).
func (k *Knowledge) SnapshotElements(env string, els []store.Element) error {
	now := k.now()
	snaps := make([]store.EnvSnapshot, 0, len(els))
	for _, e := range els {
		snaps = append(snaps, store.EnvSnapshot{
			Env:           env,
			QualifiedName: e.QualifiedName,
			ElementType:   e.ElementType,
			Name:          e.Name,
			FilePath:      e.FilePath,
			Metadata:      e.Metadata,
			CapturedAt:    now,
		})
	}
	return k.st.EnvSnapshotsPut(snaps)
}

// FindEnvConflicts reports how `service` (an element qualified name) differs
// across the standard environments:
//
//   - missing_in_env  the element has no snapshot in that env
//   - schema_version  present in two envs with different metadata.version
//   - config_drift    present in two envs, same version, different metadata
//
// Risk is HIGH for a production gap or a version mismatch, MEDIUM otherwise
// (Rust's classification).
func (k *Knowledge) FindEnvConflicts(service string) ([]EnvConflict, error) {
	type envMeta struct {
		env  string
		meta map[string]any
	}
	var present []envMeta
	conflicts := []EnvConflict{}
	for _, env := range StandardEnvs {
		snap, ok, err := k.st.EnvSnapshotGet(env, service)
		if err != nil {
			return nil, err
		}
		if !ok {
			risk := "MEDIUM"
			if env == "production" {
				risk = "HIGH"
			}
			conflicts = append(conflicts, EnvConflict{
				ConflictType: "missing_in_env",
				Detail:       fmt.Sprintf("Service '%s' is missing in %s environment", service, env),
				Risk:         risk,
			})
			continue
		}
		present = append(present, envMeta{env: env, meta: snap.Metadata})
	}

	if len(present) >= 2 {
		base := present[0]
		for _, other := range present[1:] {
			if metadataEqual(base.meta, other.meta) {
				continue
			}
			baseVersion, otherVersion := versionOf(base.meta), versionOf(other.meta)
			if baseVersion != otherVersion {
				conflicts = append(conflicts, EnvConflict{
					ConflictType: "schema_version",
					Detail: fmt.Sprintf("Version mismatch: %s has '%s', %s has '%s'",
						base.env, baseVersion, other.env, otherVersion),
					Risk: "HIGH",
				})
			} else {
				conflicts = append(conflicts, EnvConflict{
					ConflictType: "config_drift",
					Detail:       fmt.Sprintf("Metadata differs between %s and %s", base.env, other.env),
					Risk:         "MEDIUM",
				})
			}
		}
	}
	return conflicts, nil
}

// versionOf reads the metadata version label; absent or empty is "unknown",
// matching the Rust unwrap_or("unknown").
func versionOf(meta map[string]any) string {
	if v, ok := meta["version"].(string); ok && v != "" {
		return v
	}
	return "unknown"
}

// metadataEqual is the Rust `serde_json::Value !=` comparison: deep value
// equality of the two metadata objects. encoding/json sorts object keys and
// an absent map is treated as {} (the Rust column defaulted to an empty
// object), so both sides canonicalize the same way.
func metadataEqual(a, b map[string]any) bool { return canonicalMeta(a) == canonicalMeta(b) }

func canonicalMeta(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		// Unreachable for JSON-derived values; an unencodable snapshot must not
		// be reported as "equal to everything".
		return "\x00unencodable"
	}
	return string(b)
}
