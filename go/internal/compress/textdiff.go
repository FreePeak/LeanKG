package compress

import (
	"strconv"
	"strings"
)

// textdiff.go ports the diff behavior the Rust reader got from the `similar`
// crate (TextDiff::from_lines + unified_diff().context_radius(3).to_string()).
// Output semantics verified against similar 3.1.0 sources:
//   - no "---/+++" file headers (only set via .header(), which reader.rs
//     does not use),
//   - hunk header "@@ -{old} +{new} @@" with UnifiedDiffHunkRange formatting
//     (length 1 -> bare start line; length 0 -> "{start},0" anchored at the
//     line before the range; otherwise "{start+1},{len}"),
//   - hunks split when the unchanged gap exceeds 2*context_radius, with
//     leading/trailing equal runs trimmed to context_radius lines,
//   - "\\ No newline at end of file" hints for unterminated last lines.
// The algorithm is the Myers O((N+M)D) greedy diff with backtracking.

type opKind uint8

const (
	opDelete opKind = iota
	opInsert
	opEqual
)

// edit is one aligned step in forward order. oldI is the consumed old-line
// index for delete/equal; newI the consumed new-line index for insert/equal.
type edit struct {
	kind opKind
	oldI int
	newI int
}

// Bounds keeping the Myers trace memory O(depth^2) reasonable. Beyond these
// the diff degrades to a single full-replace hunk (still a valid unified
// diff, just not minimal).
//
// ponytail: documented ceiling — upgrade path is the linear-space
// divide-and-conquer Myers refinement if reader diffs ever need it.
const (
	diffDepthLimit = 2048
	diffSizeLimit  = 200_000
)

type traceRow struct {
	base int // diagonal k maps to vals[k+base]
	vals []int32
}

func (r traceRow) at(k int) int { return int(r.vals[k+r.base]) }

// lineEdits computes the forward-order edit script between the two line
// slices.
func lineEdits(oldLines, newLines []string) []edit {
	n, m := len(oldLines), len(newLines)
	if n == 0 && m == 0 {
		return nil
	}
	if n+m > diffSizeLimit {
		return fullReplaceEdits(n, m)
	}

	max := n + m
	offset := max
	v := make([]int, 2*max+2)
	// trace[i] snapshots the v state BEFORE round i+1 (covering diagonals
	// [-(i), i]); round d's backtrack therefore reads trace[d-1].
	var trace []traceRow
	found := -1
	for d := 0; d <= max; d++ {
		if d > diffDepthLimit {
			break
		}
		if d >= 1 {
			lo, hi := offset-(d-1), offset+(d-1)
			vals := make([]int32, hi-lo+1)
			for i, x := range v[lo : hi+1] {
				vals[i] = int32(x)
			}
			trace = append(trace, traceRow{base: d - 1, vals: vals})
		}
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && oldLines[x] == newLines[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		if found >= 0 {
			break
		}
	}
	if found < 0 {
		return fullReplaceEdits(n, m)
	}

	// Backtrack from (n, m) collecting moves in reverse order.
	var edits []edit
	x, y := n, m
	for d := found; d >= 1; d-- {
		row := trace[d-1]
		k := x - y
		var prevK int
		if k == -d || (k != d && row.at(k-1) < row.at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := row.at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			edits = append(edits, edit{opEqual, x - 1, y - 1})
			x--
			y--
		}
		if x == prevX {
			edits = append(edits, edit{opInsert, x, y - 1})
			y--
		} else {
			edits = append(edits, edit{opDelete, x - 1, y})
			x--
		}
	}
	for x > 0 && y > 0 {
		edits = append(edits, edit{opEqual, x - 1, y - 1})
		x--
		y--
	}
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}
	return edits
}

func fullReplaceEdits(n, m int) []edit {
	edits := make([]edit, 0, n+m)
	for i := 0; i < n; i++ {
		edits = append(edits, edit{opDelete, i, 0})
	}
	for j := 0; j < m; j++ {
		edits = append(edits, edit{opInsert, n, j})
	}
	return edits
}

// UnifiedDiff renders a unified diff between oldText and newText with the
// given context radius (Rust: diff.unified_diff().context_radius(n).
// to_string()).
func UnifiedDiff(oldText, newText string, contextRadius int) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	edits := lineEdits(oldLines, newLines)
	if len(edits) == 0 {
		return ""
	}

	type change struct {
		kind           opKind
		text           string
		oldI, newI     int
		missingNewline bool
	}
	changes := make([]change, 0, len(edits))
	oi, ni := 0, 0
	for _, e := range edits {
		switch e.kind {
		case opEqual:
			changes = append(changes, change{opEqual, oldLines[e.oldI], e.oldI, e.newI, false})
			oi, ni = e.oldI+1, e.newI+1
		case opDelete:
			missing := e.oldI == len(oldLines)-1 && !strings.HasSuffix(oldText, "\n")
			changes = append(changes, change{opDelete, oldLines[e.oldI], e.oldI, ni, missing})
			oi = e.oldI + 1
		case opInsert:
			missing := e.newI == len(newLines)-1 && !strings.HasSuffix(newText, "\n")
			changes = append(changes, change{opInsert, newLines[e.newI], oi, e.newI, missing})
			ni = e.newI + 1
		}
	}

	// Mark every line within contextRadius of a modification.
	inCtx := make([]bool, len(changes))
	for i, ch := range changes {
		if ch.kind == opEqual {
			continue
		}
		lo := i - contextRadius
		if lo < 0 {
			lo = 0
		}
		hi := i + contextRadius
		if hi > len(changes)-1 {
			hi = len(changes) - 1
		}
		for k := lo; k <= hi; k++ {
			inCtx[k] = true
		}
	}

	var b strings.Builder
	hunkRange := func(start, end int) string {
		length := end - start
		beginning := start + 1
		if length == 1 {
			return strconv.Itoa(beginning)
		}
		if length == 0 {
			// Empty ranges begin at the line just before the range
			// (similar UnifiedDiffHunkRange).
			beginning--
			return strconv.Itoa(beginning) + ",0"
		}
		return strconv.Itoa(beginning) + "," + strconv.Itoa(length)
	}

	i := 0
	for i < len(changes) {
		if !inCtx[i] {
			i++
			continue
		}
		j := i
		for j < len(changes) && inCtx[j] {
			j++
		}
		oldStart, newStart := -1, -1
		oldEnd, newEnd := 0, 0
		for _, ch := range changes[i:j] {
			if ch.kind != opInsert {
				if oldStart == -1 {
					oldStart = ch.oldI
				}
				oldEnd = ch.oldI + 1
			}
			if ch.kind != opDelete {
				if newStart == -1 {
					newStart = ch.newI
				}
				newEnd = ch.newI + 1
			}
		}
		if oldStart == -1 {
			oldStart = oi // pure insertion hunk
		}
		if newStart == -1 {
			newStart = ni // pure deletion hunk
		}
		b.WriteString("@@ -" + hunkRange(oldStart, oldEnd) + " +" + hunkRange(newStart, newEnd) + " @@\n")
		for _, ch := range changes[i:j] {
			switch ch.kind {
			case opEqual:
				b.WriteByte(' ')
			case opDelete:
				b.WriteByte('-')
			case opInsert:
				b.WriteByte('+')
			}
			b.WriteString(ch.text)
			b.WriteByte('\n')
			if ch.missingNewline {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
		i = j
	}
	return b.String()
}
