package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Flow is a stored flow: the document as it was saved, plus server state
// about it (server-v1.md §3.2).
type Flow struct {
	ID   string
	Name string
	// Document is the flow JSON exactly as saved. ListFlows leaves it nil.
	Document []byte
	// Revision is 1 on create and grows by one on every save.
	Revision int64
	// Enabled means deployed: the flow should run while the server runs.
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateFlow stores a new flow at revision 1, not enabled. The caller has
// already checked the document; the store keeps its bytes verbatim. Returns
// ErrExists if the ID is taken.
func (s *Store) CreateFlow(ctx context.Context, id, name string, document []byte) (Flow, error) {
	now := s.stamp()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO flows (id, name, document, revision, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 1, 0, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		id, name, string(document), formatTime(now), formatTime(now))
	if err != nil {
		return Flow{}, fmt.Errorf("create flow %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Flow{}, fmt.Errorf("flow %s: %w", id, ErrExists)
	}
	return Flow{
		ID: id, Name: name, Document: document, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// SaveFlow replaces a flow's document if its stored revision is still
// revision, and returns the flow at the next revision. Returns ErrNotFound
// for an unknown ID and ErrConflict when someone saved in between.
func (s *Store) SaveFlow(ctx context.Context, id, name string, document []byte, revision int64) (Flow, error) {
	now := s.stamp()
	res, err := s.db.ExecContext(ctx, `
		UPDATE flows SET name = ?, document = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?`,
		name, string(document), formatTime(now), id, revision)
	if err != nil {
		return Flow{}, fmt.Errorf("save flow %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either the flow is gone or the revision moved on.
		if _, err := s.GetFlow(ctx, id); err != nil {
			return Flow{}, err
		}
		return Flow{}, fmt.Errorf("flow %s: %w", id, ErrConflict)
	}
	return s.GetFlow(ctx, id)
}

// GetFlow returns one flow with its document, or ErrNotFound.
func (s *Store) GetFlow(ctx context.Context, id string) (Flow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, document, revision, enabled, created_at, updated_at
		FROM flows WHERE id = ?`, id)
	var f Flow
	var doc string
	if err := scanFlow(row, &f, &doc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Flow{}, fmt.Errorf("flow %s: %w", id, ErrNotFound)
		}
		return Flow{}, fmt.Errorf("get flow %s: %w", id, err)
	}
	f.Document = []byte(doc)
	return f, nil
}

// ListFlows returns every flow without its document, ordered by name, then
// ID.
func (s *Store) ListFlows(ctx context.Context) ([]Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, '', revision, enabled, created_at, updated_at
		FROM flows ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()
	flows := []Flow{}
	for rows.Next() {
		var f Flow
		var doc string
		if err := scanFlow(rows, &f, &doc); err != nil {
			return nil, fmt.Errorf("list flows: %w", err)
		}
		flows = append(flows, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	return flows, nil
}

// SetEnabled records whether a flow is deployed. Returns ErrNotFound for an
// unknown ID. It does not change the revision: deploying is not an edit.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE flows SET enabled = ? WHERE id = ?`, enabled, id)
	if err != nil {
		return fmt.Errorf("set flow %s enabled: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("flow %s: %w", id, ErrNotFound)
	}
	return nil
}

// DeleteFlow deletes a flow and, through the foreign keys, its executions
// and their trace events. Returns ErrNotFound for an unknown ID.
func (s *Store) DeleteFlow(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM flows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete flow %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("flow %s: %w", id, ErrNotFound)
	}
	return nil
}

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface {
	Scan(dest ...any) error
}

func scanFlow(sc scanner, f *Flow, doc *string) error {
	var created, updated string
	if err := sc.Scan(&f.ID, &f.Name, doc, &f.Revision, &f.Enabled, &created, &updated); err != nil {
		return err
	}
	var err error
	if f.CreatedAt, err = parseTime(created); err != nil {
		return err
	}
	f.UpdatedAt, err = parseTime(updated)
	return err
}
