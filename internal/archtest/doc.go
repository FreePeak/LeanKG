// Package archtest holds the context-propagation ratchet tests described in
// docs/plan-context-and-interfaces-refactor.md (section 5, CX-13).
//
// Each test measures the repository as it is today and compares the result
// with a committed budget. The budget can only move toward compliance:
//
//   - a count above budget fails, because a new violation was added;
//   - a count below budget also fails, because a violation was removed and
//     the budget must be lowered in the same diff, so progress is locked in;
//   - a count with no budget entry fails, because the directory is new to
//     the scan.
//
// To lower a budget, run `go test ./internal/archtest`, read the "lower the
// budget" message, and set the named map entry (ctxlessBackendMethods,
// backgroundBudget, todoBudget or ioBudget) to the reported count. Delete the
// entry when the count reaches zero.
//
// The tests parse source with go/parser and inspect the syntax tree. They are
// heuristics by design, not type-checked analysis: the rules and their known
// false positives are documented next to the rules in ctx_io_test.go.
// The shared scanner lives in scan_test.go.
package archtest
