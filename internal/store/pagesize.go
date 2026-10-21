package store

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPageSize is the page size a NEW store is created with.
//
// Measured with dbstat on the live leankg store: the `embedding_vectors`
// table held 46,936,064 bytes, of which 22,256,720 bytes — 16 % of the whole
// 138,678,272-byte store — was unused page space. The reason is arithmetic:
// with page_size=4096 the max local payload is usable−35 = 4,061 bytes, and
// one vector row is 2,134 bytes (a 2,048-byte float32 blob for 512 dims plus
// its row overhead), so TWO ROWS NEVER FIT IN ONE PAGE. Every vector owned a
// whole 4 KiB page with 1,948 bytes (48 %) of it empty.
//
// With page_size=8192 the max local payload is 8,157 bytes, so three rows fit
// per page: ~2,734 bytes per vector. Measured on a 300-vector store: 835,584
// bytes at 8192 against 1,236,992 bytes at 4096 — 32.4 % smaller, with every
// vector byte-identical.
//
// This applies to NEW stores only. SQLite adopts a new page size only while a
// VACUUM rewrites the file, and a WAL database cannot change its page size at
// all; an existing store therefore keeps its page size until an operator asks
// for the rewrite explicitly (UpgradePageSize). Rewriting a live store's file
// layout is not a decision this package may take on its own.
const DefaultPageSize = 8192

// PageSize reports the page size of the SQLite store at path.
func PageSize(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return 0, fmt.Errorf("store: open %s: %w", path, err)
	}
	defer db.Close()
	var size int
	if err := db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		return 0, fmt.Errorf("store: read page size of %s: %w", path, err)
	}
	return size, nil
}

// PageSizeUpgrade reports what one page-size rewrite did.
type PageSizeUpgrade struct {
	// Path is the store that was rewritten.
	Path string
	// Before and After are the page sizes, in bytes.
	Before int
	After  int
	// BeforeBytes and AfterBytes are the file sizes on disk.
	BeforeBytes int64
	AfterBytes  int64
	// Vectors is the number of vector rows the store held after the rewrite.
	// It is the losslessness witness: the rewrite must not lose a vector.
	Vectors int
}

// UpgradePageSize rewrites the store at path with the given page size.
//
// It is opt-in and never automatic: an operator asks for it explicitly. The
// rewrite is a whole-file VACUUM, which needs the write lock for its duration
// and needs disk space for a second copy of the file, so it belongs to a
// deliberate maintenance window, not to a background tick.
//
// The sequence is the one SQLite requires — a WAL database cannot change its
// page size, so the store leaves WAL, takes the new size, is rewritten by the
// VACUUM, and returns to WAL:
//
//	PRAGMA journal_mode = DELETE
//	PRAGMA page_size = N
//	VACUUM
//	PRAGMA journal_mode = WAL
//
// The vector rows are counted before and after: a rewrite that lost a vector
// is a bug, not a compaction, and the count is what says so.
func UpgradePageSize(path string, size int) (PageSizeUpgrade, error) {
	out := PageSizeUpgrade{Path: path}
	if size < 512 || size > 65536 || size&(size-1) != 0 {
		return out, fmt.Errorf("store: page size %d is not a power of two between 512 and 65536", size)
	}
	if _, err := os.Stat(path); err != nil {
		return out, fmt.Errorf("store: no store at %s: %w", path, err)
	}
	before, err := PageSize(path)
	if err != nil {
		return out, err
	}
	out.Before = before
	if fi, err := os.Stat(path); err == nil {
		out.BeforeBytes = fi.Size()
	}
	if before == size {
		return out, nil
	}

	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return out, fmt.Errorf("store: open %s: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	vectorsBefore, err := countVectors(db)
	if err != nil {
		return out, err
	}
	journal, err := currentJournalMode(db)
	if err != nil {
		return out, err
	}
	if journal != "delete" {
		if _, err := db.Exec(`PRAGMA journal_mode = DELETE`); err != nil {
			return out, fmt.Errorf("store: leave WAL mode: %w", err)
		}
	}
	if _, err := db.Exec(`PRAGMA page_size = ` + strconv.Itoa(size)); err != nil {
		return out, fmt.Errorf("store: set page size: %w", err)
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return out, fmt.Errorf("store: rewrite at page size %d: %w", size, err)
	}
	if journal != "delete" {
		if _, err := db.Exec(`PRAGMA journal_mode = ` + journal); err != nil {
			return out, fmt.Errorf("store: restore %s mode: %w", journal, err)
		}
	}
	after, err := PageSize(path)
	if err != nil {
		return out, err
	}
	out.After = after
	if fi, err := os.Stat(path); err == nil {
		out.AfterBytes = fi.Size()
	}
	if after != size {
		return out, fmt.Errorf("store: the rewrite left page_size %d, want %d", after, size)
	}
	vectorsAfter, err := countVectors(db)
	if err != nil {
		return out, err
	}
	out.Vectors = vectorsAfter
	if vectorsAfter != vectorsBefore {
		return out, fmt.Errorf("store: the rewrite changed the vector count: %d -> %d", vectorsBefore, vectorsAfter)
	}
	return out, nil
}

// countVectors counts the rows of the vector table, if the store has one.
func countVectors(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM embedding_vectors`).Scan(&n)
	if err != nil {
		// A store without the vector table (an empty project, a pre-vector
		// database) has nothing to lose.
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, fmt.Errorf("store: count vectors: %w", err)
	}
	return n, nil
}

// currentJournalMode reports the store's journal mode.
func currentJournalMode(db *sql.DB) (string, error) {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", fmt.Errorf("store: read journal mode: %w", err)
	}
	return mode, nil
}
