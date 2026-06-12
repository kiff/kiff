// Command server hosts the agentic-ops starter domain over HTTP.
//
// It mirrors examples/refund-agno's server: the kiff httpapi handler is
// the trust boundary, plus a tiny `/demo/agent/refund` route the agent
// uses as its single tool. The server picks the right contract per
// call, opens approvals when needed, and exposes /demo/rebuild so the
// agent can verify materialized state matches replayed state.
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

	"github.com/kiff/kiff/cmd/kiff/templates/agentic-ops/internal/domain"

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

	rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentic-ops server failed to start: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}
	if err := seedOrders(context.Background(), rt); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if *portFile != "" {
		if err := os.WriteFile(*portFile, []byte(fmt.Sprintf("%d\n", port)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "port file: %v\n", err)
			os.Exit(1)
		}
	}
	url := localURL(listener.Addr().String())
	fmt.Println("KIFF agentic-ops starter")
	fmt.Printf("- listening on %s (port=%d)\n", url, port)
	if *dataDir != "" {
		fmt.Printf("- file-backed stores at %s\n", *dataDir)
	} else {
		fmt.Println("- in-memory stores")
	}
	fmt.Println("- demo routes:")
	fmt.Println("    GET  /demo/orders")
	fmt.Println("    POST /demo/agent/refund   {order_id, amount_cents, reason, approval_id?}")
	fmt.Println("    GET  /demo/rebuild?entity=<id>")
	fmt.Println("- standard kiff routes: /events/raw, /entities/{id}/timeline, /approvals/{id}/grant|deny, ...")

	server := &http.Server{Handler: buildMux(rt), ReadHeaderTimeout: 5 * time.Second}

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
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
	<-idle
}

func buildRuntime(dataDir string) (*runtime.Runtime, func(), error) {
	if dataDir == "" {
		rt, err := domain.NewRuntime()
		return rt, nil, err
	}
	bundle, err := file.NewBundle(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open file bundle: %w", err)
	}
	storeBundle := bundle.AsStoreBundle()
	rt, err := domain.NewRuntimeWithStores(&storeBundle)
	if err != nil {
		_ = bundle.Close()
		return nil, nil, err
	}
	return rt, func() { _ = bundle.Close() }, nil
}

func buildMux(rt *runtime.Runtime) http.Handler {
	demo := newDemoHandler(rt)
	mux := http.NewServeMux()
	mux.HandleFunc("/demo/orders", demo.handleListOrders)
	mux.HandleFunc("/demo/agent/refund", demo.handleAgentRefund)
	mux.HandleFunc("/demo/rebuild", demo.handleRebuild)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", httpapi.NewHandler(rt))
	return mux
}

func seedOrders(ctx context.Context, rt *runtime.Runtime) error {
	seeds := []struct {
		id    string
		total int64
	}{
		{"order-1", 4200},
		{"order-2", 99900},
	}
	for _, seed := range seeds {
		if _, err := rt.IngestRaw(ctx, adapter.RawInput{
			ID:         "seed-evt-" + seed.id,
			Adapter:    domain.AdapterRefund,
			Type:       domain.EventOrderPlaced,
			Source:     "agentic-ops/seed",
			EntityID:   seed.id,
			EntityType: domain.EntityOrder,
			ActorID:    domain.SystemActor.ID,
			ReceivedAt: time.Now().UTC(),
			Metadata:   event.Metadata{TraceID: "seed-" + seed.id},
			Payload:    map[string]any{"total_cents": seed.total},
		}); err != nil {
			return fmt.Errorf("seed %s open: %w", seed.id, err)
		}
		markPaid, _ := rt.Actions.Get(domain.ActionMarkPaid)
		if _, err := rt.ExecuteAction(ctx, action.ActionContext{
			ActionName:   domain.ActionMarkPaid,
			EntityID:     seed.id,
			EntityType:   domain.EntityOrder,
			CurrentState: domain.StateCreated,
			Actor:        domain.SystemActor,
			Parameters:   map[string]any{"payment_id": "pay-" + seed.id},
		}, markPaid); err != nil {
			return fmt.Errorf("seed %s pay: %w", seed.id, err)
		}
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Demo handler
// ────────────────────────────────────────────────────────────────────

type demoHandler struct {
	rt *runtime.Runtime

	mu          sync.Mutex
	approvalSeq int
}

func newDemoHandler(rt *runtime.Runtime) *demoHandler {
	return &demoHandler{rt: rt}
}

type agentRefundRequest struct {
	OrderID     string  `json:"order_id"`
	AmountCents int64   `json:"amount_cents"`
	Reason      string  `json:"reason"`
	ApprovalID  string  `json:"approval_id,omitempty"`
	Reasoning   string  `json:"reasoning,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
}

type agentResponse struct {
	Outcome      string               `json:"outcome"`
	Action       string               `json:"action"`
	OrderID      string               `json:"order_id"`
	ApprovalID   string               `json:"approval_id,omitempty"`
	State        string               `json:"state,omitempty"`
	Result       *action.ActionResult `json:"result,omitempty"`
	ErrorMessage string               `json:"error,omitempty"`
}

func (h *demoHandler) nextApprovalID(orderID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.approvalSeq++
	return fmt.Sprintf("approval-%s-%d", orderID, h.approvalSeq)
}

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
	for _, id := range []string{"order-1", "order-2"} {
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

	contract, ok := h.rt.Actions.Get(domain.ActionRefundOrder)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing contract")
		return
	}
	approvalID := req.ApprovalID
	if approvalID == "" {
		approvalID = h.nextApprovalID(req.OrderID)
	}
	actionCtx := action.ActionContext{
		ActionName:   domain.ActionRefundOrder,
		EntityID:     req.OrderID,
		EntityType:   domain.EntityOrder,
		CurrentState: current.Value,
		Actor:        domain.AgentActor,
		Parameters: map[string]any{
			"amount_cents": req.AmountCents,
			"reason":       req.Reason,
		},
		ApprovalID: approvalID,
	}

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	resp := agentResponse{Action: domain.ActionRefundOrder, OrderID: req.OrderID, ApprovalID: approvalID}
	resp.State, _ = currentStateValue(r.Context(), h.rt, req.OrderID)
	if err != nil {
		if errors.Is(err, action.ErrApprovalRequired) {
			if _, reqErr := h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, fmt.Sprintf("agent reasoning: %s", req.Reasoning)); reqErr != nil && !errors.Is(reqErr, approval.ErrInvalidApproval) {
				resp.Outcome = "blocked"
				resp.ErrorMessage = reqErr.Error()
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
