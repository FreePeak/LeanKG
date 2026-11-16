package index

import (
	"context"
	"testing"
)

// extractAs extracts one file's content as lang under a background context
// and fails the test if extraction is cancelled.
func extractAs(t *testing.T, rel string, src []byte, lang string) fileElements {
	t.Helper()
	fe, err := extractFileAs(context.Background(), rel, src, lang)
	if err != nil {
		t.Fatalf("extractFileAs(%s): %v", rel, err)
	}
	return fe
}

// extractFile extracts one file's content, tagging it with the legacy static
// extension->language map.
func extractFile(t *testing.T, rel string, src []byte) fileElements {
	t.Helper()
	return extractAs(t, rel, src, extLang[extOf(rel)])
}
