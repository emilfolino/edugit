package store

import (
	"context"
	"fmt"
	"time"
)

// AuditEntry is one row of the audit log. Actor is empty when the actor
// has since been deleted or the event is not tied to a user.
type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	Target string
	Detail string
}

// Audit appends an entry. actorID 0 records no actor. Detail must never
// contain secrets.
func (s *Store) Audit(ctx context.Context, actorID int64, action, target, detail string) error {
	var actor any
	if actorID != 0 {
		actor = actorID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log(actor_id, action, target, detail) VALUES (?, ?, ?, ?)`,
		actor, action, target, detail)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// AuditEntries returns up to limit entries newest first, starting below
// beforeID when it is non-zero. A non-empty action filters by exact name.
func (s *Store) AuditEntries(ctx context.Context, beforeID int64, limit int, action string) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.at, COALESCE(u.username, ''), a.action, a.target, a.detail
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
		WHERE (? = 0 OR a.id < ?) AND (? = '' OR a.action = ?)
		ORDER BY a.id DESC LIMIT ?`,
		beforeID, beforeID, action, action, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		if e.At, err = time.Parse(timeLayout, at); err != nil {
			return nil, fmt.Errorf("parse audit time: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
