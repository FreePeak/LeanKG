package gitobj

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// HistoryCommit is one non-merge commit of HEAD's history with the files it
// changed. Date is the author date in strict ISO 8601.
type HistoryCommit struct {
	SHA         string
	Parents     []string
	AuthorName  string
	AuthorEmail string
	Date        string
	Subject     string
	Body        string
	Files       []HistoryFile
}

// HistoryFile is one path a commit touched. Binary files report zero lines.
type HistoryFile struct {
	Path       string
	Insertions int
	Deletions  int
}

// HistoryOptions bounds a History walk. Max <= 0 means no limit. Since, when
// set, is an exclusive commit id: the walk covers Since..HEAD only. After, when
// set, is a git date (passed as --since) that drops older commits.
type HistoryOptions struct {
	Max   int
	Since string
	After string
}

// Field and record separators of the log format. Neither can appear in a
// commit message in practice (they are C0 control characters).
const (
	histRecord = "\x1e"
	histField  = "\x1f"
	histEnd    = "\x1d"
)

// historyFormat renders one commit header: record separator, the fields, then
// the end marker that precedes the numstat lines git prints for the commit.
const historyFormat = "--format=" + histRecord + "%H" + histField + "%P" + histField +
	"%an" + histField + "%ae" + histField + "%aI" + histField + "%s" + histField + "%b" + histEnd

// History lists the non-merge commits reachable from HEAD (or Since..HEAD),
// newest first, each with its numstat file list. Renames are reported as a
// delete and an add (--no-renames), so a rename does not carry history across.
func History(ctx context.Context, root string, opts HistoryOptions) ([]HistoryCommit, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	args := []string{"-c", "core.quotepath=off", "log", "--no-merges", "--no-renames", "--numstat", historyFormat}
	if opts.Max > 0 {
		args = append(args, "-n", strconv.Itoa(opts.Max))
	}
	if opts.After != "" {
		args = append(args, "--since="+opts.After)
	}
	rev := "HEAD"
	if opts.Since != "" {
		rev = opts.Since + "..HEAD"
	}
	args = append(args, rev)
	out, stderr, err := runGit(ctx, root, args...)
	if err != nil {
		return nil, gitErr("git log failed", stderr, err)
	}
	return parseHistory(string(out))
}

// parseHistory reads the output of History. Each record starts with the
// record separator; the header ends at the end marker and the numstat lines
// follow it.
func parseHistory(out string) ([]HistoryCommit, error) {
	var commits []HistoryCommit
	for rec := range strings.SplitSeq(out, histRecord) {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		header, stats, ok := strings.Cut(rec, histEnd)
		if !ok {
			return nil, errors.New("gitobj: history record without end marker")
		}
		fields := strings.SplitN(header, histField, 7)
		if len(fields) != 7 {
			return nil, errors.New("gitobj: malformed history header")
		}
		c := HistoryCommit{
			SHA:         fields[0],
			AuthorName:  fields[2],
			AuthorEmail: fields[3],
			Date:        fields[4],
			Subject:     fields[5],
			Body:        strings.TrimSpace(fields[6]),
		}
		if fields[1] != "" {
			c.Parents = strings.Fields(fields[1])
		}
		for line := range strings.SplitSeq(stats, "\n") {
			f, ok, err := parseNumstat(line)
			if err != nil {
				return nil, err
			}
			if ok {
				c.Files = append(c.Files, f)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// parseNumstat reads one "<added>\t<deleted>\t<path>" line. Blank and
// non-numstat lines report ok false.
func parseNumstat(line string) (HistoryFile, bool, error) {
	parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
	if len(parts) != 3 {
		return HistoryFile{}, false, nil
	}
	f := HistoryFile{Path: parts[2]}
	var err error
	if parts[0] != "-" {
		if f.Insertions, err = strconv.Atoi(parts[0]); err != nil {
			return HistoryFile{}, false, errors.New("gitobj: numstat insertions for " + parts[2] + ": " + err.Error())
		}
	}
	if parts[1] != "-" {
		if f.Deletions, err = strconv.Atoi(parts[1]); err != nil {
			return HistoryFile{}, false, errors.New("gitobj: numstat deletions for " + parts[2] + ": " + err.Error())
		}
	}
	return f, true, nil
}

// IsAncestor reports whether commit id ancestor is reachable from HEAD. An id
// that names no commit is false, not an error: the caller treats it as a
// history that was rewritten away.
func IsAncestor(ctx context.Context, root, ancestor string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, stderr, err := runGit(ctx, root, "rev-parse", "--verify", "--quiet", ancestor+"^{commit}")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return false, nil
		}
		return false, gitErr("git rev-parse "+ancestor+" failed", stderr, err)
	}
	_, stderr, err = runGit(ctx, root, "merge-base", "--is-ancestor", ancestor, "HEAD")
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, gitErr("git merge-base failed", stderr, err)
}
