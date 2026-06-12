// Command support-ops-server hosts the breadth demo runtime over HTTP.
//
// One agent posts to `/demo/agent/decide` with a structured tool call.
// The server routes each tool to the right KIFF action, applying the
// breadth rules: refund routing (auto vs approval), consent pre-check
// for outreach, escalation, close. Standard kiff httpapi routes are
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

	supportops "github.com/kiff/kiff/examples/support-ops"
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
		fmt.Fprintf(os.Stderr, "support-ops-server failed to build runtime: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}
	if err := seedTickets(context.Background(), rt); err != nil {
		fmt.Fprintf(os.Stderr, "support-ops-server failed to seed tickets: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "support-ops-server listen failed: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "support-ops-server: cannot write port file: %v\n", err)
			os.Exit(1)
		}
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF support-ops demo server")
	fmt.Printf("- listening on %s (port=%d)\n", url, port)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores")
	}
	fmt.Println("- demo routes:")
	fmt.Println("    GET  /demo/tickets")
	fmt.Println("    POST /demo/agent/decide   {ticket_id, tool, parameters, reasoning?, confidence?, approval_id?}")
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
		fmt.Fprintf(os.Stderr, "support-ops-server failed: %v\n", err)
		os.Exit(1)
	}
	<-idle
}

func buildRuntime(dataDir string) (*supportops.Domain, *runtime.Runtime, func(), error) {
	d := supportops.New()
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

func buildMux(d *supportops.Domain, rt *runtime.Runtime) http.Handler {
	kiffHandler := httpapi.NewHandler(rt)
	demo := newDemoHandler(d, rt)

	mux := http.NewServeMux()
	mux.HandleFunc("/demo/tickets", demo.handleListTickets)
	mux.HandleFunc("/demo/agent/decide", demo.handleAgentDecide)
	mux.HandleFunc("/demo/rebuild", demo.handleRebuild)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", kiffHandler)
	return mux
}

// seedTickets opens five tickets in NEW. The agent's first action on
// each is TRIAGE_TICKET (the offline fixture and the prompt describe
// the agent picking exactly one tool per ticket; the demo does the
// triage step before the agent runs, so each ticket starts in TRIAGED).
//
// Ticket 5 is additionally moved to RESOLVED at seed time because its
// agent tool is `close_ticket`, which is only allowed in RESOLVED. The
// prompt's "close" outcome is the operator-equivalent of "the agent
// recognizes the work is done and asks to close"; the seed writes a
// resolution event so the close action passes through cleanly.
func seedTickets(ctx context.Context, rt *runtime.Runtime) error {
	seeds := []struct {
		id       string
		category string
		resolve  bool
	}{
		{"ticket-1", "billing", false},
		{"ticket-2", "billing", false},
		{"ticket-3", "outreach", false},
		{"ticket-4", "abuse", false},
		{"ticket-5", "billing", true},
	}
	for _, seed := range seeds {
		if _, err := rt.IngestRaw(ctx, adapter.RawInput{
			ID:         "seed-evt-open-" + seed.id,
			Adapter:    supportops.AdapterSupport,
			Type:       supportops.EventTicketOpened,
			Source:     "examples/support-ops/seed",
			EntityID:   seed.id,
			EntityType: supportops.EntityTicket,
			ActorID:    supportops.SystemActor.ID,
			ReceivedAt: time.Now().UTC(),
			Metadata:   event.Metadata{TraceID: "seed-" + seed.id},
			Payload:    map[string]any{"category": seed.category},
		}); err != nil {
			return fmt.Errorf("seed %s open: %w", seed.id, err)
		}
		triage, ok := rt.Actions.Get(supportops.ActionTriageTicket)
		if !ok {
			return fmt.Errorf("missing %s contract", supportops.ActionTriageTicket)
		}
		if _, err := rt.ExecuteAction(ctx, action.ActionContext{
			ActionName:   supportops.ActionTriageTicket,
			EntityID:     seed.id,
			EntityType:   supportops.EntityTicket,
			CurrentState: supportops.StateNew,
			Actor:        supportops.SystemActor,
			Parameters:   map[string]any{"category": seed.category},
		}, triage); err != nil {
			return fmt.Errorf("seed %s triage: %w", seed.id, err)
		}
		if seed.resolve {
			if err := rt.IngestEvent(ctx, event.Event{
				ID:         "seed-evt-resolved-" + seed.id,
				Type:       supportops.EventTicketResolved,
				EntityID:   seed.id,
				EntityType: supportops.EntityTicket,
				Source:     "examples/support-ops/seed",
				ActorID:    supportops.SystemActor.ID,
				OccurredAt: time.Now().UTC(),
				Payload:    map[string]any{"by": "seed"},
			}); err != nil {
				return fmt.Errorf("seed %s resolve: %w", seed.id, err)
			}
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Demo handler
// ────────────────────────────────────────────────────────────────────

type demoHandler struct {
	d  *supportops.Domain
	rt *runtime.Runtime

	mu          sync.Mutex
	approvalSeq int
}

func newDemoHandler(d *supportops.Domain, rt *runtime.Runtime) *demoHandler {
	return &demoHandler{d: d, rt: rt}
}

func (h *demoHandler) nextApprovalID(ticketID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.approvalSeq++
	return fmt.Sprintf("approval-%s-%d", ticketID, h.approvalSeq)
}

func (h *demoHandler) handleListTickets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	type ticketView struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		RefundsCents  int64  `json:"refunds_cents"`
	}
	out := []ticketView{}
	for _, id := range []string{"ticket-1", "ticket-2", "ticket-3", "ticket-4", "ticket-5"} {
		current, ok, err := h.rt.States.Current(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := ticketView{ID: id, RefundsCents: h.d.Cumulative(id)}
		if ok {
			view.State = current.Value
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": out})
}

// agentDecideRequest is the agent-facing tool call. The agent picks one
// tool per ticket; the server maps it to a KIFF action.
type agentDecideRequest struct {
	TicketID   string         `json:"ticket_id"`
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
}

// agentDecideResponse is the stable contract the agent's calling layer
// receives. Outcome strings: executed, approval_required,
// blocked_consent_missing, permission_denied, state_not_allowed,
// missing_parameter, blocked, unknown_tool.
type agentDecideResponse struct {
	Outcome      string               `json:"outcome"`
	Tool         string               `json:"tool"`
	Action       string               `json:"action"`
	TicketID     string               `json:"ticket_id"`
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
	ActionName func(d *supportops.Domain, ticketID string, params map[string]any) string
}

var toolMappings = map[string]toolMapping{
	"issue_refund": {
		Tool: "issue_refund",
		ActionName: func(d *supportops.Domain, ticketID string, params map[string]any) string {
			amount := readAmountFromParams(params, "amount_cents")
			if needs, _ := d.NeedsApprovalForRefund(ticketID, amount); needs {
				return supportops.ActionIssueRefund
			}
			return supportops.ActionAutoRefund
		},
	},
	"waive_fee":         {Tool: "waive_fee", ActionName: staticAction(supportops.ActionWaiveFee)},
	"send_outreach":     {Tool: "send_outreach", ActionName: staticAction(supportops.ActionSendOutreach)},
	"escalate_to_human": {Tool: "escalate_to_human", ActionName: staticAction(supportops.ActionEscalate)},
	"close_ticket":      {Tool: "close_ticket", ActionName: staticAction(supportops.ActionCloseTicket)},
}

func staticAction(name string) func(*supportops.Domain, string, map[string]any) string {
	return func(*supportops.Domain, string, map[string]any) string { return name }
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
	if req.TicketID == "" || req.Tool == "" {
		writeError(w, http.StatusBadRequest, "ticket_id and tool are required")
		return
	}

	mapping, ok := toolMappings[req.Tool]
	if !ok {
		writeJSON(w, http.StatusBadRequest, agentDecideResponse{
			Outcome:      "unknown_tool",
			Tool:         req.Tool,
			TicketID:     req.TicketID,
			ErrorMessage: "no such tool in this demo",
		})
		return
	}

	current, ok, err := h.rt.States.Current(r.Context(), req.TicketID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}

	// Pre-check: outreach without consent never opens an approval.
	if mapping.Tool == "send_outreach" {
		if err := supportops.CheckOutreachConsent(req.Parameters); err != nil {
			h.recordProposalIfWanted(r.Context(), req, supportops.ActionSendOutreach, "")
			resp := agentDecideResponse{
				Outcome:      "blocked_consent_missing",
				Tool:         req.Tool,
				Action:       supportops.ActionSendOutreach,
				TicketID:     req.TicketID,
				State:        current.Value,
				Reason:       err.Error(),
				ErrorMessage: err.Error(),
			}
			writeJSON(w, http.StatusBadRequest, resp)
			return
		}
	}

	actionName := mapping.ActionName(h.d, req.TicketID, req.Parameters)
	contract, ok := h.rt.Actions.Get(actionName)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing action contract: "+actionName)
		return
	}

	approvalID := req.ApprovalID
	if contract.ApprovalRequirement == action.ApprovalRequired && approvalID == "" {
		approvalID = h.nextApprovalID(req.TicketID)
	}

	actionCtx := action.ActionContext{
		ActionName:   actionName,
		EntityID:     req.TicketID,
		EntityType:   supportops.EntityTicket,
		CurrentState: current.Value,
		Actor:        supportops.AgentActor,
		Parameters:   req.Parameters,
		ApprovalID:   approvalID,
	}

	h.recordProposalIfWanted(r.Context(), req, actionName, approvalID)

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	resp := agentDecideResponse{
		Tool:       req.Tool,
		Action:     actionName,
		TicketID:   req.TicketID,
		ApprovalID: approvalID,
	}
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.TicketID)
	if err != nil {
		if errors.Is(err, action.ErrApprovalRequired) {
			if _, reqErr := h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, fmt.Sprintf("agent reasoning: %s", req.Reasoning)); reqErr != nil && !errors.Is(reqErr, approval.ErrInvalidApproval) {
				resp.Outcome = "blocked"
				resp.ErrorMessage = "request approval: " + reqErr.Error()
				writeJSON(w, http.StatusInternalServerError, resp)
				return
			}
			resp.Outcome = "approval_required"
			needs, reason := h.d.NeedsApprovalForRefund(req.TicketID, readAmountFromParams(req.Parameters, "amount_cents"))
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
		id = fmt.Sprintf("prop-%s-%d", req.TicketID, time.Now().UnixNano())
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
	case errors.Is(err, supportops.ErrConsentMissing):
		return "blocked_consent_missing"
	default:
		return "blocked"
	}
}

func statusForOutcome(outcome string) int {
	switch outcome {
	case "permission_denied":
		return http.StatusForbidden
	case "state_not_allowed", "missing_parameter", "blocked_consent_missing":
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
