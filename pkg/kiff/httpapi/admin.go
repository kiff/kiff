package httpapi

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
)

// Admin routes are read-only HTML views over the runtime. They exist so a
// developer can poke at a running KIFF system without writing any UI code.
//
//	GET /admin                          index of entities and pending approvals
//	GET /admin/entities/{entityID}      timeline + approvals for one entity
//
// The HTML is intentionally plain. Production deployments should put auth in
// front of these routes, just like everything else under httpapi.

func (h *Handler) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	entities, err := h.collectEntities(ctx)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	pending, err := h.collectPendingApprovals(ctx)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}

	domainName := ""
	if h.Runtime.Domain != nil {
		domainName = h.Runtime.Domain.Name
	}

	data := adminIndexData{
		DomainName: domainName,
		Entities:   entities,
		Pending:    pending,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminIndexTmpl.Execute(w, data); err != nil {
		writeError(w, http.StatusInternalServerError, "render admin index: "+err.Error())
		return
	}
}

func (h *Handler) handleAdminEntity(w http.ResponseWriter, r *http.Request) {
	entityID := strings.TrimPrefix(r.URL.Path, "/admin/entities/")
	entityID = strings.TrimSuffix(entityID, "/")
	if entityID == "" {
		writeError(w, http.StatusNotFound, "entity id is required")
		return
	}

	timeline, err := h.Runtime.Timeline(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	approvals, err := h.collectApprovalsForEntity(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	currentState := ""
	if h.Runtime.States != nil {
		st, _, err := h.Runtime.States.Current(r.Context(), entityID)
		if err == nil {
			currentState = st.Value
		}
	}

	data := adminEntityData{
		EntityID:     entityID,
		CurrentState: currentState,
		Timeline:     timeline,
		Approvals:    approvals,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminEntityTmpl.Execute(w, data); err != nil {
		writeError(w, http.StatusInternalServerError, "render admin entity: "+err.Error())
		return
	}
}

// collectEntities derives the set of known entities from the audit store.
// The audit store is queried with an empty filter, which returns every
// record. We bucket by (entity_id, entity_type) and remember the most recent
// audit kind per entity to display the operational state at a glance.
func (h *Handler) collectEntities(ctx context.Context) ([]entityRow, error) {
	if h.Runtime.Audit == nil {
		return nil, nil
	}
	records, err := h.Runtime.Audit.Query(ctx, audit.Filter{})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*entityRow)
	for _, r := range records {
		row, ok := byID[r.EntityID]
		if !ok {
			row = &entityRow{
				EntityID:   r.EntityID,
				EntityType: r.EntityType,
			}
			byID[r.EntityID] = row
		}
		row.Records++
		if r.CreatedAt.After(row.LastSeen) {
			row.LastSeen = r.CreatedAt
			row.LastKind = string(r.Kind)
		}
	}
	out := make([]entityRow, 0, len(byID))
	for _, row := range byID {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out, nil
}

func (h *Handler) collectPendingApprovals(ctx context.Context) ([]approval.Approval, error) {
	if h.Runtime.Approvals == nil {
		return nil, nil
	}
	all, err := h.Runtime.Approvals.List(ctx, "")
	if err != nil {
		return nil, err
	}
	pending := make([]approval.Approval, 0, len(all))
	for _, a := range all {
		if a.Status == approval.StatusPending {
			pending = append(pending, a)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})
	return pending, nil
}

func (h *Handler) collectApprovalsForEntity(ctx context.Context, entityID string) ([]approval.Approval, error) {
	if h.Runtime.Approvals == nil {
		return nil, nil
	}
	all, err := h.Runtime.Approvals.List(ctx, entityID)
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	return all, nil
}

// entityRow is one entry on the admin index.
type entityRow struct {
	EntityID   string
	EntityType string
	Records    int
	LastSeen   time.Time
	LastKind   string
}

type adminIndexData struct {
	DomainName string
	Entities   []entityRow
	Pending    []approval.Approval
}

type adminEntityData struct {
	EntityID     string
	CurrentState string
	Timeline     []audit.Record
	Approvals    []approval.Approval
}

// adminFuncs gives the templates a few small helpers without inventing a
// templating language.
var adminFuncs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05Z")
	},
	"truncate": func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
	"join": strings.Join,
}

const adminBaseStyle = `
<style>
  body { font: 14px/1.5 -apple-system, system-ui, sans-serif; max-width: 960px; margin: 2rem auto; padding: 0 1rem; color: #1a1a1a; }
  h1, h2 { font-weight: 600; }
  h1 { margin-bottom: 0.25rem; }
  .subtitle { color: #666; margin-top: 0; margin-bottom: 2rem; }
  table { width: 100%; border-collapse: collapse; margin: 1rem 0 2rem; }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid #e6e6e6; vertical-align: top; }
  th { font-weight: 600; color: #444; background: #fafafa; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  td.kind { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
  td.kind.warn { color: #b25000; }
  td.kind.deny { color: #b00020; font-weight: 600; }
  td.dim { color: #888; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
  .badge { display: inline-block; padding: 0.1rem 0.5rem; border-radius: 999px; font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; }
  .badge.pending { background: #fff4d6; color: #7a5300; }
  .badge.granted { background: #d6f4dc; color: #14532d; }
  .badge.denied  { background: #fde0e3; color: #80111c; }
  a { color: #0046cc; text-decoration: none; }
  a:hover { text-decoration: underline; }
  .empty { color: #888; font-style: italic; padding: 1rem 0; }
  nav { margin-bottom: 1.5rem; font-size: 13px; color: #666; }
  nav a { margin-right: 0.5rem; }
</style>`

var adminIndexTmpl = template.Must(template.New("admin-index").Funcs(adminFuncs).Parse(`
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KIFF admin{{if .DomainName}} — {{.DomainName}}{{end}}</title>
  ` + adminBaseStyle + `
</head>
<body>
  <nav><a href="/admin">/admin</a></nav>
  <h1>KIFF admin{{if .DomainName}} <small style="font-weight:400;color:#888;">— {{.DomainName}}</small>{{end}}</h1>
  <p class="subtitle">Read-only view over the runtime. Entities derive from the audit store; approvals derive from the approval store.</p>

  <h2>Pending approvals</h2>
  {{if .Pending}}
    <table>
      <thead><tr><th>Approval ID</th><th>Entity</th><th>Action</th><th>Requested by</th><th>Created</th></tr></thead>
      <tbody>
      {{range .Pending}}
        <tr>
          <td class="dim">{{.ID}}</td>
          <td><a href="/admin/entities/{{.EntityID}}">{{.EntityID}}</a> <span class="dim">({{.EntityType}})</span></td>
          <td class="kind">{{.ActionName}}</td>
          <td>{{.RequestedBy}}</td>
          <td class="dim">{{fmtTime .CreatedAt}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  {{else}}
    <p class="empty">No pending approvals.</p>
  {{end}}

  <h2>Entities</h2>
  {{if .Entities}}
    <table>
      <thead><tr><th>Entity</th><th>Type</th><th>Last fact</th><th>Last seen (UTC)</th><th>Records</th></tr></thead>
      <tbody>
      {{range .Entities}}
        <tr>
          <td><a href="/admin/entities/{{.EntityID}}">{{.EntityID}}</a></td>
          <td class="dim">{{.EntityType}}</td>
          <td class="kind">{{.LastKind}}</td>
          <td class="dim">{{fmtTime .LastSeen}}</td>
          <td class="dim">{{.Records}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  {{else}}
    <p class="empty">No entities yet. Send an event to <code>POST /events/raw</code> to get started.</p>
  {{end}}
</body>
</html>`))

var adminEntityTmpl = template.Must(template.New("admin-entity").Funcs(adminFuncs).Parse(`
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KIFF admin — {{.EntityID}}</title>
  ` + adminBaseStyle + `
</head>
<body>
  <nav><a href="/admin">/admin</a> &raquo; {{.EntityID}}</nav>
  <h1>{{.EntityID}}</h1>
  <p class="subtitle">{{if .CurrentState}}Current state: <strong>{{.CurrentState}}</strong>.{{else}}Current state unavailable.{{end}}</p>

  <h2>Approvals</h2>
  {{if .Approvals}}
    <table>
      <thead><tr><th>Approval ID</th><th>Action</th><th>Status</th><th>Requested by</th><th>Reviewed by</th><th>Reason</th><th>Created</th></tr></thead>
      <tbody>
      {{range .Approvals}}
        <tr>
          <td class="dim">{{.ID}}</td>
          <td class="kind">{{.ActionName}}</td>
          <td>
            {{if eq (printf "%s" .Status) "granted"}}
              <span class="badge granted">granted</span>
            {{else if eq (printf "%s" .Status) "denied"}}
              <span class="badge denied">denied</span>
            {{else}}
              <span class="badge pending">{{.Status}}</span>
            {{end}}
          </td>
          <td>{{.RequestedBy}}</td>
          <td>{{if .ReviewedBy}}{{.ReviewedBy}}{{else}}<span class="dim">—</span>{{end}}</td>
          <td>{{truncate .Reason 80}}</td>
          <td class="dim">{{fmtTime .CreatedAt}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  {{else}}
    <p class="empty">No approvals for this entity.</p>
  {{end}}

  <h2>Timeline</h2>
  {{if .Timeline}}
    <table>
      <thead><tr><th>When (UTC)</th><th>Kind</th><th>Actor</th><th>Message</th><th>Trace</th></tr></thead>
      <tbody>
      {{range .Timeline}}
        <tr>
          <td class="dim">{{fmtTime .CreatedAt}}</td>
          <td class="kind {{if eq (printf "%s" .Kind) "approval_denied"}}deny{{else if eq (printf "%s" .Kind) "action_failed"}}deny{{else if eq (printf "%s" .Kind) "approval_required"}}warn{{end}}">{{.Kind}}</td>
          <td>{{.ActorID}}</td>
          <td>{{truncate .Message 100}}</td>
          <td class="dim">{{if .TraceID}}{{.TraceID}}{{else}}—{{end}}</td>
        </tr>
      {{end}}
      </tbody>
    </table>
  {{else}}
    <p class="empty">No audit records for this entity.</p>
  {{end}}
</body>
</html>`))

// formatRouter is unused but kept here as documentation of the route shapes
// recognized by the admin view, so the code search "/admin/entities" finds
// the convention even if the routing changes shape later.
//
//nolint:unused
func formatRouter() string { return fmt.Sprintf("/admin and /admin/entities/{id}") }
