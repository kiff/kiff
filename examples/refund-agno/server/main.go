// Command refund-agno-server hosts the demo runtime over HTTP.
//
// It wraps the standard kiff httpapi.Handler with three demo-only routes:
//
//   - GET  /demo/orders                    list seeded orders + current state
//   - POST /demo/agent/refund              the agent's "refund_order" tool
//                                          surface; the server routes to
//                                          AUTO_REFUND or REFUND_ORDER based
//                                          on amount and returns a stable
//                                          outcome string the agent can react
//                                          to without seeing the runtime
//   - POST /demo/agent/waive               the same shape but for WAIVE_FEE
//
// The kiff routes (POST /events/raw, GET /entities/{id}/timeline,
// /entities/{id}/actions/{name}/execute, /approvals/{id}/grant|deny, ...)
// are exposed as-is so a curl-flavored prospect can poke them too.
//
// Three orders are seeded on startup:
//
//   order-1 paid 4200    cents (small refund will auto-execute)
//   order-2 paid 99900   cents (large refund — KIFF will demand approval)
//   order-3 paid 25000   cents (above ceiling; same approval gate)
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
	"strings"
	"sync"
	"syscall"
	"time"

	refundagno "github.com/kiff/kiff/examples/refund-agno"
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
	portFile := flag.String("port-file", "", "If set, write the chosen port to this file (used by Makefile)")
	flag.Parse()

	rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refund-agno-server failed to build runtime: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}

	if err := seedOrders(context.Background(), rt); err != nil {
		fmt.Fprintf(os.Stderr, "refund-agno-server failed to seed orders: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refund-agno-server listen failed: %v\n", err)
		os.Exit(1)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "refund-agno-server: cannot write port file: %v\n", err)
			os.Exit(1)
		}
	}

	url := localURL(listener.Addr().String())
	fmt.Println("KIFF refund-agno demo server")
	fmt.Printf("- listening on %s (port=%d)\n", url, port)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores")
	}
	fmt.Println("- demo routes:")
	fmt.Println("    GET  /demo/orders")
	fmt.Println("    POST /demo/agent/refund   {order_id, amount_cents, reason, approval_id?}")
	fmt.Println("    POST /demo/agent/waive    {order_id, fee_cents, reason, approval_id?}")
	fmt.Println("- standard kiff routes: /events/raw, /entities/{id}/timeline, /approvals/{id}/grant|deny, ...")

	mux := buildMux(rt)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
		close(idleConnsClosed)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "refund-agno-server failed: %v\n", err)
		os.Exit(1)
	}
	<-idleConnsClosed
}

// buildRuntime returns a refund-agno runtime configured with either
// in-memory stores (dataDir empty) or file-backed JSONL stores.
func buildRuntime(dataDir string) (*runtime.Runtime, func(), error) {
	if dataDir == "" {
		rt, err := refundagno.NewRuntime()
		return rt, nil, err
	}
	bundle, err := file.NewBundle(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open file bundle: %w", err)
	}
	storeBundle := bundle.AsStoreBundle()
	rt, err := refundagno.NewRuntimeWithStores(&storeBundle)
	if err != nil {
		_ = bundle.Close()
		return nil, nil, err
	}
	return rt, func() { _ = bundle.Close() }, nil
}

// buildMux composes the kiff httpapi handler with the demo routes that the
// agent talks to.
func buildMux(rt *runtime.Runtime) http.Handler {
	kiffHandler := httpapi.NewHandler(rt)
	demo := newDemoHandler(rt)

	mux := http.NewServeMux()
	mux.HandleFunc("/demo/orders", demo.handleListOrders)
	mux.HandleFunc("/demo/agent/refund", demo.handleAgentRefund)
	mux.HandleFunc("/demo/agent/waive", demo.handleAgentWaive)
	mux.HandleFunc("/demo/rebuild", demo.handleRebuild)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Everything else falls through to the kiff API.
	mux.Handle("/", kiffHandler)
	return mux
}

// seedOrders ingests three ORDER_PLACED events and immediately marks each
// order paid, so a fresh server has three orders sitting in PAID.
func seedOrders(ctx context.Context, rt *runtime.Runtime) error {
	seeds := []struct {
		id    string
		total int64
	}{
		{"order-1", 4200},
		{"order-2", 99900},
		{"order-3", 25000},
	}
	for _, seed := range seeds {
		if _, err := rt.IngestRaw(ctx, adapter.RawInput{
			ID:         "seed-evt-placed-" + seed.id,
			Adapter:    refundagno.AdapterRefund,
			Type:       refundagno.EventOrderPlaced,
			Source:     "examples/refund-agno/seed",
			EntityID:   seed.id,
			EntityType: refundagno.EntityOrder,
			ActorID:    refundagno.SystemActor.ID,
			ReceivedAt: time.Now().UTC(),
			Metadata:   event.Metadata{TraceID: "seed-" + seed.id},
			Payload:    map[string]any{"total_cents": seed.total},
		}); err != nil {
			return fmt.Errorf("seed %s ORDER_PLACED: %w", seed.id, err)
		}
		markPaid, ok := rt.Actions.Get(refundagno.ActionMarkPaid)
		if !ok {
			return fmt.Errorf("missing %s contract", refundagno.ActionMarkPaid)
		}
		if _, err := rt.ExecuteAction(ctx, action.ActionContext{
			ActionName:   refundagno.ActionMarkPaid,
			EntityID:     seed.id,
			EntityType:   refundagno.EntityOrder,
			CurrentState: refundagno.StateCreated,
			Actor:        refundagno.SystemActor,
			Parameters:   map[string]any{"payment_id": "pay-" + seed.id},
		}, markPaid); err != nil {
			return fmt.Errorf("seed %s MARK_PAID: %w", seed.id, err)
		}
	}
	return nil
}

// demoHandler holds the runtime and serves the small surface the agent
// uses. It is decoupled from kiffHandler so demo routes never accidentally
// shadow kiff API paths.
type demoHandler struct {
	rt *runtime.Runtime

	mu          sync.Mutex
	approvalSeq int
}

func newDemoHandler(rt *runtime.Runtime) *demoHandler {
	return &demoHandler{rt: rt}
}

// handleRebuild calls runtime.RebuildState for an entity and returns both
// the rebuilt and materialized state values so the demo's "rebuild check"
// is one HTTP call. The prompt requires that materialized == replayed for
// every order at the end of the demo.
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
		"entity_id":      entityID,
		"materialized":   materialized,
		"replayed":       replay.State.Value,
		"events_replayed": len(replay.Steps),
		"matches":        materialized == replay.State.Value,
	})
}

// handleListOrders returns the three seeded orders with their current
// state. Useful to verify the server is up and seeded.
func (h *demoHandler) handleListOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	type orderView struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	out := []orderView{}
	for _, id := range []string{"order-1", "order-2", "order-3"} {
		current, ok, err := h.rt.States.Current(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := orderView{ID: id}
		if ok {
			view.State = current.Value
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// agentRefundRequest is the JSON the agent posts as its "refund_order" tool.
type agentRefundRequest struct {
	OrderID     string  `json:"order_id"`
	AmountCents int64   `json:"amount_cents"`
	Reason      string  `json:"reason"`
	ApprovalID  string  `json:"approval_id,omitempty"`
	Reasoning   string  `json:"reasoning,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
}

// agentResponse is what the agent receives. The Outcome string is stable
// and intended to be model-friendly: "executed", "approval_required",
// "permission_denied", "state_not_allowed", "missing_parameter", "blocked".
type agentResponse struct {
	Outcome      string               `json:"outcome"`
	Action       string               `json:"action"`
	OrderID      string               `json:"order_id"`
	ApprovalID   string               `json:"approval_id,omitempty"`
	State        string               `json:"state,omitempty"`
	Result       *action.ActionResult `json:"result,omitempty"`
	ErrorMessage string               `json:"error,omitempty"`
}

func (h *demoHandler) handleAgentRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	defer r.Body.Close()
	var req agentRefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.OrderID == "" || req.AmountCents <= 0 || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "order_id, amount_cents, reason are required")
		return
	}

	current, ok, err := h.rt.States.Current(r.Context(), req.OrderID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	actionName := refundagno.RouteRefund(req.AmountCents)
	contract, ok := h.rt.Actions.Get(actionName)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing action contract: "+actionName)
		return
	}

	approvalID := req.ApprovalID
	if contract.ApprovalRequirement == action.ApprovalRequired && approvalID == "" {
		approvalID = h.nextApprovalID(req.OrderID)
	}

	actionCtx := action.ActionContext{
		ActionName:   actionName,
		EntityID:     req.OrderID,
		EntityType:   refundagno.EntityOrder,
		CurrentState: current.Value,
		Actor:        refundagno.AgentActor,
		Parameters: map[string]any{
			"amount_cents": req.AmountCents,
			"reason":       req.Reason,
		},
		ApprovalID: approvalID,
	}

	h.recordProposal(r.Context(), req, actionName, approvalID)

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	resp := agentResponse{
		Action:     actionName,
		OrderID:    req.OrderID,
		ApprovalID: approvalID,
	}
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.OrderID)
	if err != nil {
		if errors.Is(err, action.ErrApprovalRequired) {
			// Open the approval for the operator. The agent should react
			// to "approval_required" by escalating; KIFF holds the gate.
			if _, reqErr := h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, fmt.Sprintf("agent reasoning: %s", req.Reasoning)); reqErr != nil && !errors.Is(reqErr, approval.ErrInvalidApproval) {
				resp.Outcome = "blocked"
				resp.ErrorMessage = "request approval: " + reqErr.Error()
				writeJSON(w, http.StatusInternalServerError, resp)
				return
			}
			resp.Outcome = "approval_required"
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
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.OrderID)
	writeJSON(w, http.StatusOK, resp)
}

type agentWaiveRequest struct {
	OrderID    string  `json:"order_id"`
	FeeCents   int64   `json:"fee_cents"`
	Reason     string  `json:"reason"`
	ApprovalID string  `json:"approval_id,omitempty"`
	Reasoning  string  `json:"reasoning,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

func (h *demoHandler) handleAgentWaive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	defer r.Body.Close()
	var req agentWaiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.OrderID == "" || req.FeeCents <= 0 || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "order_id, fee_cents, reason are required")
		return
	}

	current, ok, err := h.rt.States.Current(r.Context(), req.OrderID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}

	contract, ok := h.rt.Actions.Get(refundagno.ActionWaiveFee)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing action contract: "+refundagno.ActionWaiveFee)
		return
	}

	approvalID := req.ApprovalID
	if approvalID == "" {
		approvalID = h.nextApprovalID(req.OrderID)
	}

	actionCtx := action.ActionContext{
		ActionName:   refundagno.ActionWaiveFee,
		EntityID:     req.OrderID,
		EntityType:   refundagno.EntityOrder,
		CurrentState: current.Value,
		Actor:        refundagno.AgentActor,
		Parameters: map[string]any{
			"fee_cents": req.FeeCents,
			"reason":    req.Reason,
		},
		ApprovalID: approvalID,
	}

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	resp := agentResponse{
		Action:     refundagno.ActionWaiveFee,
		OrderID:    req.OrderID,
		ApprovalID: approvalID,
	}
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.OrderID)
	if err != nil {
		if errors.Is(err, action.ErrApprovalRequired) {
			if _, reqErr := h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, fmt.Sprintf("agent reasoning: %s", req.Reasoning)); reqErr != nil && !errors.Is(reqErr, approval.ErrInvalidApproval) {
				resp.Outcome = "blocked"
				resp.ErrorMessage = "request approval: " + reqErr.Error()
				writeJSON(w, http.StatusInternalServerError, resp)
				return
			}
			resp.Outcome = "approval_required"
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

// nextApprovalID hands out a stable per-order approval id. The id is
// monotonically incrementing per server lifetime so multiple denied
// attempts can each have a distinct approval.
func (h *demoHandler) nextApprovalID(orderID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.approvalSeq++
	return fmt.Sprintf("approval-%s-%d", orderID, h.approvalSeq)
}

// recordProposal stores the agent's reasoning/confidence as a KIFF decision
// before the action runs. We deliberately do not block the action on the
// outcome of this write; it is best-effort context capture.
func (h *demoHandler) recordProposal(ctx context.Context, req agentRefundRequest, actionName, approvalID string) {
	if req.Reasoning == "" && req.Confidence == 0 {
		return
	}
	id := strings.ReplaceAll(approvalID, "approval-", "prop-")
	if id == "" {
		id = fmt.Sprintf("prop-%s-%d", req.OrderID, time.Now().UnixNano())
	}
	_ = h.rt.RecordActionProposal(ctx, proposalFromRequest(id, req, actionName))
}

// classifyOutcome turns a runtime error into a stable, model-friendly
// outcome string. Mirrors the contract used by examples/llm-bridge.
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
	default:
		return "blocked"
	}
}

func statusForOutcome(outcome string) int {
	switch outcome {
	case "permission_denied":
		return http.StatusForbidden
	case "state_not_allowed", "missing_parameter":
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
