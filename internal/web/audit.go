package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/store"
)

// Auditor records and lists audit entries. *store.Store implements it.
type Auditor interface {
	Audit(ctx context.Context, actorID int64, action, target, detail string) error
	AuditEntries(ctx context.Context, beforeID int64, limit int, action string) ([]store.AuditEntry, error)
}

const auditPageSize = 50

// audit logs a security-relevant action and records it in the audit log. A
// failure to record never blocks the action itself, but is logged.
func (s *Server) audit(r *http.Request, u store.User, action, target, detail string) {
	s.log.Info(action, "target", target, "by", u.Username)
	if s.opts.Audit == nil {
		return
	}
	if err := s.opts.Audit.Audit(r.Context(), u.ID, action, target, detail); err != nil {
		s.log.Error("audit", "err", err)
	}
}

// auditLog serves the admin-only audit viewer, newest first.
func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	pr, err := s.principal(r.Context(), u)
	if err != nil {
		s.fail(w, "load principal", err)
		return
	}
	if !authz.Can(pr, authz.AuditView, authz.Resource{}) {
		http.NotFound(w, r)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	filter := r.URL.Query().Get("action")
	entries, err := s.opts.Audit.AuditEntries(r.Context(), before, auditPageSize+1, filter)
	if err != nil {
		s.fail(w, "list audit", err)
		return
	}
	p := s.newPage(r, "Audit log")
	p.Filter = filter
	if len(entries) > auditPageSize {
		entries = entries[:auditPageSize]
		p.Next = entries[len(entries)-1].ID
	}
	p.Entries = entries
	s.render(w, "audit.html", p, http.StatusOK)
}
