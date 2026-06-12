// Command ai-cafe-ops-server hosts the operational-authority demo
// runtime over HTTP.
//
// One AI shift manager posts to `/demo/agent/decide` with a structured
// tool call. The server routes each tool to the right KIFF action,
// applying the breadth rules: order routing (auto vs approval), catalog
// pre-check for specialty requests, working-hours pre-check for staff
// messages, supplier escalation. Standard kiff httpapi routes are
// also exposed unchanged.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	aicafeops "github.com/kiff/kiff/examples/ai-cafe-ops"
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/httpapi"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/store/file"
)

func main() {
	addr := flag.String("addr", ":0", "HTTP listen address; :0 picks a free port")
	dataDir := flag.String("data-dir", "", "Directory for file-backed JSONL stores; empty uses in-memory stores")
	portFile := flag.String("port-file", "", "If set, write the chosen port to this file")
	flag.Parse()

	d, rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-cafe-ops-server failed to build runtime: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}
	if err := seedShifts(context.Background(), rt); err != nil {
		fmt.Fprintf(os.Stderr, "ai-cafe-ops-server failed to seed shifts: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-cafe-ops-server listen failed: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "ai-cafe-ops-server: cannot write port file: %v\n", err)
			os.Exit(1)
		}
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF ai-cafe-ops demo server")
	fmt.Printf("- listening on %s (port=%d)\n", url, port)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores")
	}
	fmt.Println("- demo routes:")
	fmt.Println("    GET  /demo/shifts")
	fmt.Println("    GET  /demo/catalog")
	fmt.Println("    POST /demo/agent/decide   {shift_id, tool, parameters, reasoning?, confidence?, approval_id?}")
	fmt.Println("    GET  /demo/rebuild?entity=<id>")

	mux := buildMux(d, rt)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
		close(idle)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "ai-cafe-ops-server failed: %v\n", err)
		os.Exit(1)
	}
	<-idle
}

func buildRuntime(dataDir string) (*aicafeops.Domain, *runtime.Runtime, func(), error) {
	d := aicafeops.New()
	if dataDir == "" {
		rt, err := d.NewRuntime()
		return d, rt, nil, err
	}
	bundle, err := file.NewBundle(dataDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open file bundle: %w", err)
	}
	storeBundle := bundle.AsStoreBundle()
	rt, err := d.NewRuntimeWithStores(&storeBundle)
	if err != nil {
		_ = bundle.Close()
		return nil, nil, nil, err
	}
	return d, rt, func() { _ = bundle.Close() }, nil
}

func buildMux(d *aicafeops.Domain, rt *runtime.Runtime) http.Handler {
	kiffHandler := httpapi.NewHandler(rt)
	demo := newDemoHandler(d, rt)

	mux := http.NewServeMux()
	mux.HandleFunc("/demo/shifts", demo.handleListShifts)
	mux.HandleFunc("/demo/catalog", demo.handleCatalog)
	mux.HandleFunc("/demo/agent/decide", demo.handleAgentDecide)
	mux.HandleFunc("/demo/rebuild", demo.handleRebuild)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", kiffHandler)
	return mux
}

// seedShifts opens five shifts. The agent's first action on each is
// START_SHIFT (the offline fixture and the prompt describe the agent
// picking exactly one tool per shift; the demo runs START_SHIFT before
// the agent runs, so each shift starts in OPEN).
func seedShifts(ctx context.Context, rt *runtime.Runtime) error {
	seeds := []struct {
		id       string
		openedBy string
	}{
		{"shift-1", "shift-manager"},
		{"shift-2", "shift-manager"},
		{"shift-3", "shift-manager"},
		{"shift-4", "shift-manager"},
		{"shift-5", "shift-manager"},
	}
	for _, seed := range seeds {
		if _, err := rt.IngestRaw(ctx, adapter.RawInput{
			ID:         "seed-evt-scheduled-" + seed.id,
			Adapter:    aicafeops.AdapterCafe,
			Type:       aicafeops.EventShiftScheduled,
			Source:     "examples/ai-cafe-ops/seed",
			EntityID:   seed.id,
			EntityType: aicafeops.EntityShift,
			ActorID:    aicafeops.SystemActor.ID,
			ReceivedAt: time.Now().UTC(),
			Metadata:   event.Metadata{TraceID: "seed-" + seed.id},
			Payload:    map[string]any{"opened_by": seed.openedBy},
		}); err != nil {
			return fmt.Errorf("seed %s scheduled: %w", seed.id, err)
		}
		startContract, ok := rt.Actions.Get(aicafeops.ActionStartShift)
		if !ok {
			return fmt.Errorf("missing %s contract", aicafeops.ActionStartShift)
		}
		if _, err := rt.ExecuteAction(ctx, action.ActionContext{
			ActionName:   aicafeops.ActionStartShift,
			EntityID:     seed.id,
			EntityType:   aicafeops.EntityShift,
			CurrentState: aicafeops.StateNew,
			Actor:        aicafeops.SystemActor,
			Parameters:   map[string]any{"opened_by": seed.openedBy},
		}, startContract); err != nil {
			return fmt.Errorf("seed %s start: %w", seed.id, err)
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Demo handler
// ────────────────────────────────────────────────────────────────────

type demoHandler struct {
	d  *aicafeops.Domain
	rt *runtime.Runtime

	mu          sync.Mutex
	approvalSeq int
}

func newDemoHandler(d *aicafeops.Domain, rt *runtime.Runtime) *demoHandler {
	return &demoHandler{d: d, rt: rt}
}

func (h *demoHandler) nextApprovalID(shiftID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.approvalSeq++
	return fmt.Sprintf("approval-%s-%d", shiftID, h.approvalSeq)
}

func (h *demoHandler) handleListShifts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	type shiftView struct {
		ID           string `json:"id"`
		State        string `json:"state"`
		OrdersCents  int64  `json:"orders_cents"`
	}
	out := []shiftView{}
	for _, id := range []string{"shift-1", "shift-2", "shift-3", "shift-4", "shift-5"} {
		current, ok, err := h.rt.States.Current(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := shiftView{ID: id, OrdersCents: h.d.Cumulative(id)}
		if ok {
			view.State = current.Value
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"shifts": out})
}

func (h *demoHandler) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	startHour, endHour := h.d.WorkingHours()
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog":             h.d.CatalogList(),
		"working_hours_start": startHour,
		"working_hours_end":   endHour,
	})
}

// agentDecideRequest is the agent-facing tool call. The agent picks one
// tool per shift; the server maps it to a KIFF action.
type agentDecideRequest struct {
	ShiftID    string         `json:"shift_id"`
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
}

// agentDecideResponse is the stable contract the agent's calling layer
// receives. Outcome strings: executed, approval_required,
// blocked_not_in_catalog, blocked_after_hours, permission_denied,
// state_not_allowed, missing_parameter, blocked, unknown_tool.
type agentDecideResponse struct {
	Outcome      string               `json:"outcome"`
	Tool         string               `json:"tool"`
	Action       string               `json:"action"`
	ShiftID      string               `json:"shift_id"`
	ApprovalID   string               `json:"approval_id,omitempty"`
	State        string               `json:"state,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Result       *action.ActionResult `json:"result,omitempty"`
	ErrorMessage string               `json:"error,omitempty"`
}

// toolMapping records how an agent-facing tool maps to a KIFF action.
// The mapping is data so the server stays small and the table is
// trivial to read.
type toolMapping struct {
	Tool       string
	ActionName func(d *aicafeops.Domain, shiftID string, params map[string]any) string
}

var toolMappings = map[string]toolMapping{
	"order_inventory": {
		Tool: "order_inventory",
		ActionName: func(d *aicafeops.Domain, shiftID string, params map[string]any) string {
			amount := readAmountFromParams(params, "amount_cents")
			if needs, _ := d.NeedsApprovalForOrder(shiftID, amount); needs {
				return aicafeops.ActionOrderInventory
			}
			return aicafeops.ActionAutoOrderInventory
		},
	},
	"request_specialty":  {Tool: "request_specialty", ActionName: staticAction(aicafeops.ActionRequestSpecialty)},
	"send_staff_message": {Tool: "send_staff_message", ActionName: staticAction(aicafeops.ActionSendStaffMessage)},
	"escalate_supplier":  {Tool: "escalate_supplier", ActionName: staticAction(aicafeops.ActionEscalateSupplier)},
}

func staticAction(name string) func(*aicafeops.Domain, string, map[string]any) string {
	return func(*aicafeops.Domain, string, map[string]any) string { return name }
}

func readAmountFromParams(params map[string]any, key string) int64 {
	if params == nil {
		return 0
	}
	switch v := params[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

func (h *demoHandler) handleAgentDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	defer r.Body.Close()
	var req agentDecideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.ShiftID == "" || req.Tool == "" {
		writeError(w, http.StatusBadRequest, "shift_id and tool are required")
		return
	}

	mapping, ok := toolMappings[req.Tool]
	if !ok {
		writeJSON(w, http.StatusBadRequest, agentDecideResponse{
			Outcome:      "unknown_tool",
			Tool:         req.Tool,
			ShiftID:      req.ShiftID,
			ErrorMessage: "no such tool in this demo",
		})
		return
	}

	current, ok, err := h.rt.States.Current(r.Context(), req.ShiftID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "shift not found")
		return
	}

	// Pre-check: specialty without catalog entry never opens an approval.
	if mapping.Tool == "request_specialty" {
		if err := h.d.CheckCatalog(req.Parameters); err != nil {
			h.recordProposalIfWanted(r.Context(), req, aicafeops.ActionRequestSpecialty, "")
			resp := agentDecideResponse{
				Outcome:      "blocked_not_in_catalog",
				Tool:         req.Tool,
				Action:       aicafeops.ActionRequestSpecialty,
				ShiftID:      req.ShiftID,
				State:        current.Value,
				Reason:       err.Error(),
				ErrorMessage: err.Error(),
			}
			writeJSON(w, http.StatusBadRequest, resp)
			return
		}
	}

	// Pre-check: staff messages outside working hours never open an approval.
	if mapping.Tool == "send_staff_message" {
		if err := h.d.CheckWorkingHours(req.Parameters); err != nil {
			h.recordProposalIfWanted(r.Context(), req, aicafeops.ActionSendStaffMessage, "")
			resp := agentDecideResponse{
				Outcome:      "blocked_after_hours",
				Tool:         req.Tool,
				Action:       aicafeops.ActionSendStaffMessage,
				ShiftID:      req.ShiftID,
				State:        current.Value,
				Reason:       err.Error(),
				ErrorMessage: err.Error(),
			}
			writeJSON(w, http.StatusBadRequest, resp)
			return
		}
	}

	actionName := mapping.ActionName(h.d, req.ShiftID, req.Parameters)
	contract, ok := h.rt.Actions.Get(actionName)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing action contract: "+actionName)
		return
	}

	approvalID := req.ApprovalID
	if contract.ApprovalRequirement == action.ApprovalRequired && approvalID == "" {
		approvalID = h.nextApprovalID(req.ShiftID)
	}

	actionCtx := action.ActionContext{
		ActionName:   actionName,
		EntityID:     req.ShiftID,
		EntityType:   aicafeops.EntityShift,
		CurrentState: current.Value,
		Actor:        aicafeops.AgentActor,
		Parameters:   req.Parameters,
		ApprovalID:   approvalID,
	}

	h.recordProposalIfWanted(r.Context(), req, actionName, approvalID)

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	resp := agentDecideResponse{
		Tool:       req.Tool,
		Action:     actionName,
		ShiftID:    req.ShiftID,
		ApprovalID: approvalID,
	}
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.ShiftID)
	if err != nil {
		if errors.Is(err, action.ErrApprovalRequired) {
			if _, reqErr := h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, fmt.Sprintf("agent reasoning: %s", req.Reasoning)); reqErr != nil && !errors.Is(reqErr, approval.ErrInvalidApproval) {
				resp.Outcome = "blocked"
				resp.ErrorMessage = "request approval: " + reqErr.Error()
				writeJSON(w, http.StatusInternalServerError, resp)
				return
			}
			resp.Outcome = "approval_required"
			needs, reason := h.d.NeedsApprovalForOrder(req.ShiftID, readAmountFromParams(req.Parameters, "amount_cents"))
			if needs {
				resp.Reason = reason
			}
			resp.ErrorMessage = err.Error()
			writeJSON(w, http.StatusAccepted, resp)
			return
		}
		resp.Outcome = classifyOutcome(err)
		resp.ErrorMessage = err.Error()
		writeJSON(w, statusForOutcome(resp.Outcome), resp)
		return
	}
	resp.Outcome = "executed"
	resp.Result = &res
	writeJSON(w, http.StatusOK, resp)
}

func (h *demoHandler) recordProposalIfWanted(ctx context.Context, req agentDecideRequest, actionName, approvalID string) {
	if req.Reasoning == "" && req.Confidence == 0 {
		return
	}
	id := approvalID
	if id == "" {
		id = fmt.Sprintf("prop-%s-%d", req.ShiftID, time.Now().UnixNano())
	}
	id = "prop-" + id
	_ = h.rt.RecordActionProposal(ctx, proposalFromRequest(id, req, actionName))
}

func (h *demoHandler) handleRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	entityID := r.URL.Query().Get("entity")
	if entityID == "" {
		writeError(w, http.StatusBadRequest, "entity query param is required")
		return
	}
	current, ok, err := h.rt.States.Current(r.Context(), entityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	materialized := ""
	if ok {
		materialized = current.Value
	}
	replay, err := h.rt.RebuildState(r.Context(), entityID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entity_id":       entityID,
		"materialized":    materialized,
		"replayed":        replay.State.Value,
		"events_replayed": len(replay.Steps),
		"matches":         materialized == replay.State.Value,
	})
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

func classifyOutcome(err error) string {
	switch {
	case errors.Is(err, action.ErrApprovalRequired):
		return "approval_required"
	case errors.Is(err, action.ErrPermissionDenied):
		return "permission_denied"
	case errors.Is(err, action.ErrStateNotAllowed):
		return "state_not_allowed"
	case errors.Is(err, action.ErrMissingParameter):
		return "missing_parameter"
	case errors.Is(err, aicafeops.ErrNotInCatalog):
		return "blocked_not_in_catalog"
	case errors.Is(err, aicafeops.ErrAfterHours):
		return "blocked_after_hours"
	default:
		return "blocked"
	}
}

func statusForOutcome(outcome string) int {
	switch outcome {
	case "permission_denied":
		return http.StatusForbidden
	case "state_not_allowed", "missing_parameter", "blocked_not_in_catalog", "blocked_after_hours":
		return http.StatusBadRequest
	default:
		return http.StatusUnprocessableEntity
	}
}

func currentStateValue(ctx context.Context, rt *runtime.Runtime, entityID string) (string, bool) {
	current, ok, err := rt.States.Current(ctx, entityID)
	if err != nil || !ok {
		return "", false
	}
	return current.Value, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func localURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "::", "[::]", "0.0.0.0":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
