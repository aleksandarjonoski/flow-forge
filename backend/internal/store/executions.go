package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is how an execution stands or ended.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	// StatusInterrupted marks an execution that was still running when the
	// server stopped without finishing it (a crash or a kill).
	StatusInterrupted Status = "interrupted"
)

// ErrBadCursor is returned by ListExecutions for a cursor it did not issue.
var ErrBadCursor = errors.New("invalid cursor")

// Execution is one recorded run of a deployed flow (server-v1.md §5.1).
type Execution struct {
	ID       string
	FlowID   string
	Revision int64
	Trigger  string
	Status   Status
	Error    string
	// StartedAt is when the execution began; FinishedAt is zero while it
	// runs, and stays zero for an interrupted execution.
	StartedAt  time.Time
	FinishedAt time.Time
	// TraceComplete is false when node events were dropped under load.
	TraceComplete bool
}

// TraceRecord is one stored trace event: its position in the execution and
// the event as JSON, in the API's shape.
type TraceRecord struct {
	ExecutionID string
	Seq         int
	Event       json.RawMessage
}

// Finish is the end of an execution.
type Finish struct {
	ID         string
	Status     Status
	Error      string
	FinishedAt time.Time
}

// Batch is a set of history writes applied in one transaction. They are
// applied in field order, so an execution may start and finish in the same
// batch.
type Batch struct {
	Started  []Execution
	Events   []TraceRecord
	Finished []Finish
	// Incomplete lists executions that lost node events (server-v1.md §5.2).
	Incomplete []string
}

// WriteBatch applies b in one transaction. Writes for a flow or execution
// that no longer exists (deleted, or pruned, while its writes were queued)
// are skipped rather than failing the batch.
func (s *Store) WriteBatch(ctx context.Context, b Batch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	defer tx.Rollback()

	for _, e := range b.Started {
		var finished any
		if !e.FinishedAt.IsZero() {
			finished = formatTime(e.FinishedAt)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO executions
			  (id, flow_id, revision, trigger_id, status, error, started_at, finished_at, trace_complete)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
			WHERE EXISTS (SELECT 1 FROM flows WHERE id = ?)`,
			e.ID, e.FlowID, e.Revision, e.Trigger, e.Status, nullString(e.Error),
			formatTime(e.StartedAt), finished, e.TraceComplete, e.FlowID); err != nil {
			return fmt.Errorf("write execution %s: %w", e.ID, err)
		}
	}
	for _, ev := range b.Events {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trace_events (execution_id, seq, event)
			SELECT ?, ?, ?
			WHERE EXISTS (SELECT 1 FROM executions WHERE id = ?)`,
			ev.ExecutionID, ev.Seq, string(ev.Event), ev.ExecutionID); err != nil {
			return fmt.Errorf("write trace event %s/%d: %w", ev.ExecutionID, ev.Seq, err)
		}
	}
	for _, f := range b.Finished {
		if _, err := tx.ExecContext(ctx, `
			UPDATE executions SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
			f.Status, nullString(f.Error), formatTime(f.FinishedAt), f.ID); err != nil {
			return fmt.Errorf("finish execution %s: %w", f.ID, err)
		}
	}
	for _, id := range b.Incomplete {
		if _, err := tx.ExecContext(ctx, `
			UPDATE executions SET trace_complete = 0 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("mark execution %s incomplete: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

// ListExecutions returns up to limit executions of a flow, newest first,
// starting after cursor ("" for the first page). next is the cursor for the
// following page, or "" when there is none.
func (s *Store) ListExecutions(ctx context.Context, flowID string, limit int, cursor string) (page []Execution, next string, err error) {
	if limit < 1 {
		return nil, "", fmt.Errorf("list executions: limit must be positive")
	}
	query := `SELECT ` + executionColumns + ` FROM executions WHERE flow_id = ?`
	args := []any{flowID}
	if cursor != "" {
		started, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (started_at < ? OR (started_at = ? AND id < ?))`
		args = append(args, started, started, id)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()
	page = []Execution{}
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list executions: %w", err)
		}
		page = append(page, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list executions: %w", err)
	}
	if len(page) > limit {
		page = page[:limit]
		last := page[limit-1]
		next = encodeCursor(formatTime(last.StartedAt), last.ID)
	}
	return page, next, nil
}

// GetExecution returns one execution and its trace events in order, or
// ErrNotFound.
func (s *Store) GetExecution(ctx context.Context, id string) (Execution, []json.RawMessage, error) {
	e, err := scanExecution(s.db.QueryRowContext(ctx,
		`SELECT `+executionColumns+` FROM executions WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Execution{}, nil, fmt.Errorf("execution %s: %w", id, ErrNotFound)
		}
		return Execution{}, nil, fmt.Errorf("get execution %s: %w", id, err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT event FROM trace_events WHERE execution_id = ? ORDER BY seq`, id)
	if err != nil {
		return Execution{}, nil, fmt.Errorf("get trace %s: %w", id, err)
	}
	defer rows.Close()
	trace := []json.RawMessage{}
	for rows.Next() {
		var ev string
		if err := rows.Scan(&ev); err != nil {
			return Execution{}, nil, fmt.Errorf("get trace %s: %w", id, err)
		}
		trace = append(trace, json.RawMessage(ev))
	}
	if err := rows.Err(); err != nil {
		return Execution{}, nil, fmt.Errorf("get trace %s: %w", id, err)
	}
	return e, trace, nil
}

// MarkInterrupted marks every execution still running as interrupted. The
// server calls it on start, before any flow runs: such executions were cut
// off by a crash or kill (server-v1.md §4.5). Returns how many it marked.
func (s *Store) MarkInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE executions SET status = ? WHERE status = ?`, StatusInterrupted, StatusRunning)
	if err != nil {
		return 0, fmt.Errorf("mark interrupted executions: %w", err)
	}
	return res.RowsAffected()
}

// Prune keeps the newest keep executions of each flow and deletes the rest,
// with their trace events. keep < 1 keeps everything. Returns how many
// executions it deleted.
func (s *Store) Prune(ctx context.Context, keep int) (int64, error) {
	if keep < 1 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM executions WHERE id IN (
		  SELECT id FROM (
		    SELECT id, ROW_NUMBER() OVER (
		      PARTITION BY flow_id ORDER BY started_at DESC, id DESC) AS n
		    FROM executions)
		  WHERE n > ?)`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune executions: %w", err)
	}
	return res.RowsAffected()
}

const executionColumns = `id, flow_id, revision, trigger_id, status, error, started_at, finished_at, trace_complete`

func scanExecution(sc scanner) (Execution, error) {
	var e Execution
	var errMsg, finished sql.NullString
	var started string
	if err := sc.Scan(&e.ID, &e.FlowID, &e.Revision, &e.Trigger, &e.Status, &errMsg,
		&started, &finished, &e.TraceComplete); err != nil {
		return Execution{}, err
	}
	e.Error = errMsg.String
	var err error
	if e.StartedAt, err = parseTime(started); err != nil {
		return Execution{}, err
	}
	if finished.Valid {
		if e.FinishedAt, err = parseTime(finished.String); err != nil {
			return Execution{}, err
		}
	}
	return e, nil
}

// nullString stores "" as NULL.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// A cursor is the last row's start time and ID, opaque to clients.
func encodeCursor(started, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(started + "\n" + id))
}

func decodeCursor(cursor string) (started, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", ErrBadCursor
	}
	started, id, ok := strings.Cut(string(raw), "\n")
	if !ok {
		return "", "", ErrBadCursor
	}
	if _, err := parseTime(started); err != nil {
		return "", "", ErrBadCursor
	}
	return started, id, nil
}
