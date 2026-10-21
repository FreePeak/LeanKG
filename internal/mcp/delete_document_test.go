package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMemoryTestServer builds a server over a project whose memory handle the
// test keeps, so a fixture can write bank rows directly.
func newMemoryTestServer(t *testing.T) (*mcp.ClientSession, *memory.Memory) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mem.Close() })
	engine := core.New(st, mem, nil)
	engine.SetProjectDir(dir)
	srv := New(engine)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session, mem
}

// bankRowCount counts the rows in a bank file.
func bankRowCount(t *testing.T, mem *memory.Memory, bank string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mem.Root(), "banks", bank+".jsonl"))
	if err != nil {
		t.Fatalf("read bank %s: %v", bank, err)
	}
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// TestImportDeleteDocumentRemovesOneBankRow pins C.2: the only delete in the
// whole system was the REST route (DELETE /v1/default/banks/{bank}/
// documents/{id}), so an MCP client — the primary surface for the omp
// memory backend — could never remove a memory row.
//
// The call removes exactly the row it names and leaves the rest.
func TestImportDeleteDocumentRemovesOneBankRow(t *testing.T) {
	session, mem := newMemoryTestServer(t)
	ctx := context.Background()

	if err := mem.RetainRaw("bank", []memory.Entry{
		{Content: "the deploy runbook lives in docs/deploy.md", Metadata: map[string]any{"document_id": "doc-a"}},
		{Content: "postgres holds the vectors in pgvector", Metadata: map[string]any{"document_id": "doc-b"}},
	}); err != nil {
		t.Fatalf("retain: %v", err)
	}
	if n := bankRowCount(t, mem, "bank"); n != 2 {
		t.Fatalf("fixture holds %d rows, want 2", n)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"action":      "memory",
			"command":     "delete_document",
			"document_id": "doc-a",
			"bank":        "bank",
		},
	})
	if err != nil {
		t.Fatalf("delete_document: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("delete_document result = %v, want ok:true", out)
	}
	if n, _ := out["memory_units_deleted"].(float64); n != 1 {
		t.Fatalf("memory_units_deleted = %v, want 1", out["memory_units_deleted"])
	}
	if n := bankRowCount(t, mem, "bank"); n != 1 {
		t.Fatalf("bank holds %d rows after the delete, want 1", n)
	}
	// The row that survived is the one that was not named.
	entries, err := mem.Recall("bank", "postgres vectors", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(entries) != 1 || entries[0].Content != "postgres holds the vectors in pgvector" {
		t.Fatalf("surviving rows = %v, want the doc-b row only", entries)
	}
}

// TestImportDeleteDocumentRequiresADocumentID pins the validation: a delete
// without a document id names nothing, so it is refused instead of silently
// removing nothing.
func TestImportDeleteDocumentRequiresADocumentID(t *testing.T) {
	session, mem := newMemoryTestServer(t)
	ctx := context.Background()
	if err := mem.RetainRaw("bank", []memory.Entry{{Content: "a row"}}); err != nil {
		t.Fatalf("retain: %v", err)
	}
	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"action":  "memory",
			"command": "delete_document",
			"bank":    "bank",
		},
	})
	if err == nil {
		t.Fatal("delete_document without a document_id must be refused")
	}
	if !strings.Contains(err.Error(), "document_id") {
		t.Fatalf("error = %v, want it to name the missing document_id", err)
	}
	if n := bankRowCount(t, mem, "bank"); n != 1 {
		t.Fatalf("a refused delete changed the bank: %d rows", n)
	}
}

// TestImportSchemaAdvertisesDeleteDocument: the command must be in the
// advertised enumeration, or a model cannot find it.
func TestImportSchemaAdvertisesDeleteDocument(t *testing.T) {
	session := newTestServer(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != core.ToolImport {
			continue
		}
		schema := fmt.Sprintf("%v", tool.InputSchema)
		if !strings.Contains(schema, "delete_document") {
			t.Fatalf("the import schema does not advertise delete_document: %s", schema)
		}
		return
	}
	t.Fatal("import tool not registered")
}
