// Package envfile reads and appends KEY=VALUE lines in a dotenv-style file.
// AppendMissing never rewrites an existing line: it only adds keys the file
// does not define yet, through an atomic temp-file rename, and leaves the file
// mode at 0600 (the file typically holds a plaintext credential).
package envfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pair is one assignment. Keys are plain identifiers; values are written raw
// when they are plain, single-quoted otherwise.
type Pair struct {
	Key   string
	Value string
}

// Load parses the file at path into a key/value map. Blank lines and # comments
// are skipped; an optional `export ` prefix and one pair of matching single or
// double quotes around the value are accepted. A missing file is an empty map.
func Load(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("envfile: read %s: %w", path, err)
	}
	return parse(string(data)), nil
}

// AppendMissing appends `KEY=VALUE` for each pair whose key the file does not
// define, in the order given. Existing content is kept byte for byte, and
// nothing is written when every key is already defined. A file that lacks a
// trailing newline gets one before the first appended line.
func AppendMissing(path string, pairs []Pair) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("envfile: read %s: %w", path, err)
	}
	defined := parse(string(existing))

	var added strings.Builder
	for _, p := range pairs {
		if _, ok := defined[p.Key]; ok {
			continue
		}
		value, err := formatValue(p.Value)
		if err != nil {
			return fmt.Errorf("envfile: %s: %w", p.Key, err)
		}
		added.WriteString(p.Key + "=" + value + "\n")
	}
	if added.Len() == 0 {
		return nil
	}

	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeAtomic(path, content+added.String())
}

func parse(data string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out
}

// formatValue returns the value as it must appear after `=`. Values that are
// not plain are single-quoted; a value holding a quote or a line break has no
// faithful encoding in this format and is refused rather than mangled.
func formatValue(v string) (string, error) {
	if strings.ContainsAny(v, "'\r\n") {
		return "", errors.New("value holds a quote or line break and cannot be written")
	}
	if !isPlain(v) {
		return "'" + v + "'", nil
	}
	return v, nil
}

func isPlain(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-@/:+=,", r):
		default:
			return false
		}
	}
	return true
}

// writeAtomic writes content to a temp file beside path, sets mode 0600, and
// renames it over path, so a reader sees either the old or the new file.
func writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".envfile-*")
	if err != nil {
		return fmt.Errorf("envfile: temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("envfile: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("envfile: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("envfile: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("envfile: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("envfile: rename into %s: %w", path, err)
	}
	return nil
}
