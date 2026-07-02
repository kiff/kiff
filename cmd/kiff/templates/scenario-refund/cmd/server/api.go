package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/kiff/kiff/cmd/kiff/templates/scenario-refund/domain"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// apiHandler is the generated app's headless API: the surface an agent (or any
// HTTP client) calls to operate the app. Every mutation goes through the KIFF
// runtime, and the business side effect runs only after the runtime allows the
// action. The routes, tool manifest, and OpenAPI document are all derived from
// the runtime's action catalog, so they cannot drift from the domain.
//
// This is distinct from the KIFF governance API (mounted at / by httpapi):
// that is the runtime/governance surface; this is the application's own
// action/tool surface, already governed by KIFF.
type apiHandler struct {
	rt     *runtime.Runtime
	ledger *ledger

	mu  sync.Mutex
	seq int
}

func newAPIHandler(rt *runtime.Runtime, l *ledger) *apiHandler {
	return &apiHandler{rt: rt, ledger: l}
}

func (h *apiHandler) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/actions", h.handleActions)
	mux.HandleFunc("/api/openapi.json", h.handleOpenAPI)
	mux.HandleFunc("/api/tools/manifest.json", h.handleManifest)
	mux.HandleFunc("/api/tools/", h.handleToolCall)     // POST /api/tools/{tool}
	mux.HandleFunc("/api/entities/", h.handleEntity)    // GET /api/entities/{id}[/timeline]
	mux.HandleFunc("/api/approvals/", h.handleApproval) // POST /api/approvals/{id}/grant|deny
}

func (h *apiHandler) nextApprovalID(entityID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	return "appr-" + entityID + "-" + itoa(h.seq)
}

// toolName is the LLM-facing name for an action: its lowercased form.
// REFUND_ORDER <-> refund_order. Deriving it from the action name keeps the
// manifest and the routes in sync with the catalog.
//
// Note: this round-trip assumes UPPER_SNAKE action names (the scaffold
// convention). A mixed-case action name would not round-trip through
// ToLower/ToUpper and its tool route would 404.
func toolName(actionName string) string { return strings.ToLower(actionName) }
func actionFromTool(tool string) string { return strings.ToUpper(tool) }

type toolCallRequest struct {
	EntityID   string         `json:"entity_id"`
	Parameters map[string]any `json:"parameters"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
}

// handleToolCall is the governed side-effect path: POST /api/tools/{tool}.
// It validates and executes through KIFF and returns a normalized decision
// envelope. The business side effect runs only on the allowed path.
func (h *apiHandler) handleToolCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	tool := strings.TrimPrefix(r.URL.Path, "/api/tools/")
	if tool == "" || strings.Contains(tool, "/") {
		writeError(w, http.StatusNotFound, "tool name required")
		return
	}
	actionName := actionFromTool(tool)
	contract, ok := h.rt.Actions.Get(actionName)
	if !ok {
		writeJSON(w, http.StatusNotFound, outcome.UnknownAction(actionName, ""))
		return
	}
	defer r.Body.Close()
	var req toolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.EntityID == "" {
		writeError(w, http.StatusBadRequest, "entity_id is required")
		return
	}
	current, found, err := h.rt.States.Current(r.Context(), req.EntityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	approvalID := req.ApprovalID
	if approvalID == "" {
		approvalID = h.nextApprovalID(req.EntityID)
	}
	actionCtx := action.ActionContext{
		ActionName:   actionName,
		EntityID:     req.EntityID,
		EntityType:   domain.EntityOrder,
		CurrentState: current.Value,
		Actor:        domain.AgentActor,
		Parameters:   req.Parameters,
		ApprovalID:   approvalID,
	}

	res, execErr := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	if execErr != nil {
		d := outcome.FromError(execErr, actionName, req.EntityID, current.Value)
		if d.Outcome == outcome.ApprovalRequired {
			_, _ = h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, req.Reasoning)
		}
		writeJSON(w, statusForOutcome(d.Outcome), toolResponse(d, approvalID, nil))
		return
	}

	// Allowed. The business side effect runs now, and only now.
	h.applySideEffect(actionName, req)
	post := current.Value
	if cur, ok, err := h.rt.States.Current(r.Context(), req.EntityID); err == nil && ok {
		post = cur.Value
	}
	d := outcome.Succeeded(actionName, req.EntityID, post)
	writeJSON(w, http.StatusOK, toolResponse(d, approvalID, &res))
}

// applySideEffect performs the mock business write for an allowed action. In a
// real app this is your database write or payments call. It is reached only
// after ExecuteAction succeeds, so it cannot run on a blocked action.
func (h *apiHandler) applySideEffect(actionName string, req toolCallRequest) {
	if actionName != domain.ActionRefundOrder {
		return
	}
	amount, _ := domain.ReadIntCents(req.Parameters, "amount_cents")
	reason, _ := req.Parameters["reason"].(string)
	h.ledger.record(refundRecord{OrderID: req.EntityID, AmountCents: amount, Reason: reason, Guarded: true})
}

func (h *apiHandler) handleEntity(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/entities/")
	if rest == "" {
		writeError(w, http.StatusNotFound, "entity id required")
		return
	}
	if id, ok := strings.CutSuffix(rest, "/timeline"); ok {
		records, err := h.rt.Timeline(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entity_id": id, "timeline": records})
		return
	}
	current, found, err := h.rt.States.Current(r.Context(), rest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entity_id": rest, "state": current.Value})
}

func (h *apiHandler) handleApproval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/approvals/")
	var status approval.Status
	var id string
	switch {
	case strings.HasSuffix(rest, "/grant"):
		id, status = strings.TrimSuffix(rest, "/grant"), approval.StatusGranted
	case strings.HasSuffix(rest, "/deny"):
		id, status = strings.TrimSuffix(rest, "/deny"), approval.StatusDenied
	default:
		writeError(w, http.StatusNotFound, "use /api/approvals/{id}/grant or /deny")
		return
	}
	reviewed, err := h.rt.ReviewApproval(r.Context(), id, domain.OperatorActor.ID, status, "reviewed via app API")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": reviewed})
}

func (h *apiHandler) handleActions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"actions": toolDescriptors(h.rt)})
}

func (h *apiHandler) handleManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tools": toolDescriptors(h.rt)})
}

func (h *apiHandler) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, openAPIDoc(h.rt))
}

func toolResponse(d outcome.Decision, approvalID string, res *action.ActionResult) map[string]any {
	body := map[string]any{
		"outcome":   d.Outcome,
		"action":    d.Action,
		"tool":      toolName(d.Action),
		"entity_id": d.EntityID,
		"state":     d.CurrentState,
	}
	if d.Reason != "" {
		body["reason"] = d.Reason
	}
	if d.NextStep != "" {
		body["next_step"] = d.NextStep
	}
	if d.Message != "" {
		body["error"] = d.Message
	}
	if d.Outcome == outcome.ApprovalRequired {
		body["approval_id"] = approvalID
	}
	if res != nil {
		body["result"] = res
	}
	return body
}

// itoa avoids importing strconv just for a small counter.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
