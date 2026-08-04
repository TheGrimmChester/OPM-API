package main

import "strings"

// Board column ↔ GitHub Issue state mapping.
//
// GitHub Issues have exactly two states (open, closed) while the OPM board has
// six columns, so the mapping is deliberately asymmetric and is declared here
// rather than inferred at the call site.
//
// Push (OPM → GitHub) is total: every column has exactly one issue state.
//
//	backlog      → open
//	queue        → open
//	in_progress  → open
//	review       → open
//	human_review → open
//	done         → closed
//
// Pull (GitHub → OPM) is lossy and therefore conservative. "closed" identifies a
// single column, but "open" matches five, so an open issue carries no column
// information:
//
//	closed → move the task to `done`
//	open   → move to issueReopenColumn ONLY if the task currently sits in `done`
//	         (the issue was reopened); otherwise leave the column untouched.
//
// Refusing to guess on the open→column direction is what keeps a pull from
// dragging a task in `human_review` back to `in_progress` on every refresh.
var boardColumnIssueState = map[string]string{
	"backlog":      "open",
	"queue":        "open",
	"in_progress":  "open",
	"review":       "open",
	"human_review": "open",
	"done":         "closed",
}

// issueClosedColumn is where a closed issue puts the task.
const issueClosedColumn = "done"

// issueReopenColumn is where a task returns when a closed issue is reopened.
const issueReopenColumn = "in_progress"

// issueStateForColumn returns the GitHub state for an OPM board column.
// ok is false for a column outside BoardColumns, so callers push nothing rather
// than defaulting to a state the operator did not ask for.
func issueStateForColumn(column string) (state string, ok bool) {
	state, ok = boardColumnIssueState[strings.TrimSpace(column)]
	return
}

// columnForIssueState resolves the board column a task should occupy given the
// issue state and the task's current column. move is false when the mapping
// carries no instruction, which is the common case for an open issue.
func columnForIssueState(issueState, currentColumn string) (column string, move bool) {
	issueState = strings.TrimSpace(strings.ToLower(issueState))
	currentColumn = strings.TrimSpace(currentColumn)
	switch issueState {
	case "closed":
		if currentColumn == issueClosedColumn {
			return "", false
		}
		return issueClosedColumn, true
	case "open":
		if currentColumn == issueClosedColumn {
			return issueReopenColumn, true
		}
		return "", false
	default:
		return "", false
	}
}
