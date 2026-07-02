// Command server hosts the refund scenario over HTTP.
//
// The KIFF httpapi handler is the governance surface (mounted at /). Around it
// sit a few demo routes that make the enablement story runnable with curl:
//
//	GET  /demo/orders                 list seeded orders and their state
//	POST /demo/agent/refund           refund THROUGH KIFF (the governed path)
//	POST /demo/unguarded/refund       refund with NO governance (the danger)
//	GET  /demo/ledger                 the mock business side effects recorded
//	GET  /demo/rebuild?entity=<id>    replay: materialized vs event-derived state
//
// The agent-facing tool (`/demo/agent/refund`) never touches the business
// side effect directly — it asks the KIFF runtime to decide, and only a
// runtime-allowed action reaches the ledger. That is the boundary the demo
// exists to show.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kiff/kiff/cmd/kiff/templates/scenario-refund/domain"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/httpapi"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/store/file"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address; :0 picks a free port")
	dataDir := flag.String("data-dir", "", "Directory for file-backed JSONL stores; empty uses in-memory")
	portFile := flag.String("port-file", "", "If set, write the chosen port to this file")
	flag.Parse()

	rt, closer, err := buildRuntime(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server failed to start: %v\n", err)
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
	fmt.Printf("refund scenario listening on %s\n", localURL(listener.Addr().String()))
	fmt.Println("routes: /demo/orders, /demo/agent/refund, /demo/unguarded/refund, /demo/ledger, /demo/rebuild")
	fmt.Println("NOTE: the demo API is unauthenticated. Add auth before exposing it beyond localhost.")

	server := &http.Server{Handler: buildMux(rt), ReadHeaderTimeout: 5 * time.Second}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
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
	sb := bundle.AsStoreBundle()
	rt, err := domain.NewRuntimeWithStores(&sb)
	if err != nil {
		_ = bundle.Close()
		return nil, nil, err
	}
	return rt, func() { _ = bundle.Close() }, nil
}

func buildMux(rt *runtime.Runtime) http.Handler {
	d := &demoHandler{rt: rt, ledger: &ledger{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/demo/orders", d.handleListOrders)
	mux.HandleFunc("/demo/agent/refund", d.handleGuardedRefund)
	mux.HandleFunc("/demo/unguarded/refund", d.handleUnguardedRefund)
	mux.HandleFunc("/demo/ledger", d.handleLedger)
	mux.HandleFunc("/demo/rebuild", d.handleRebuild)
	mux.Handle("/", httpapi.NewHandler(rt))
	return mux
}

var seededOrders = []struct {
	id    string
	total int64
}{
	{"order-1", 4200},
	{"order-2", 99900},
}

func seedOrders(ctx context.Context, rt *runtime.Runtime) error {
	for _, s := range seededOrders {
		if _, err := rt.IngestRaw(ctx, adapter.RawInput{
			ID:         "seed-" + s.id,
			Adapter:    domain.AdapterRefund,
			Type:       domain.EventOrderPlaced,
			Source:     "scenario/seed",
			EntityID:   s.id,
			EntityType: domain.EntityOrder,
			ActorID:    domain.SystemActor.ID,
			ReceivedAt: time.Now().UTC(),
			Metadata:   event.Metadata{TraceID: "seed-" + s.id},
			Payload:    map[string]any{"total_cents": s.total},
		}); err != nil {
			return fmt.Errorf("seed %s: %w", s.id, err)
		}
		markPaid, _ := rt.Actions.Get(domain.ActionMarkPaid)
		if _, err := rt.ExecuteAction(ctx, action.ActionContext{
			ActionName:   domain.ActionMarkPaid,
			EntityID:     s.id,
			EntityType:   domain.EntityOrder,
			CurrentState: domain.StateCreated,
			Actor:        domain.SystemActor,
			Parameters:   map[string]any{"payment_id": "pay-" + s.id},
		}, markPaid); err != nil {
			return fmt.Errorf("seed %s pay: %w", s.id, err)
		}
	}
	return nil
}

type demoHandler struct {
	rt     *runtime.Runtime
	ledger *ledger

	mu  sync.Mutex
	seq int
}

func (h *demoHandler) nextApprovalID(orderID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	return fmt.Sprintf("approval-%s-%d", orderID, h.seq)
}

type refundRequest struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
	ApprovalID  string `json:"approval_id,omitempty"`
	Reasoning   string `json:"reasoning,omitempty"`
}

func (h *demoHandler) handleListOrders(w http.ResponseWriter, r *http.Request) {
	type view struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		RefundCents int64  `json:"refunded_cents"`
	}
	out := []view{}
	for _, s := range seededOrders {
		current, ok, err := h.rt.States.Current(r.Context(), s.id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		v := view{ID: s.id, RefundCents: h.ledger.totalForOrder(s.id)}
		if ok {
			v.State = current.Value
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// handleGuardedRefund routes the refund through KIFF. The ledger is written
// only after the runtime allows the action.
func (h *demoHandler) handleGuardedRefund(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeRefund(w, r)
	if !ok {
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
		Parameters:   map[string]any{"amount_cents": req.AmountCents, "reason": req.Reason},
		ApprovalID:   approvalID,
	}

	res, err := h.rt.ExecuteAction(r.Context(), actionCtx, contract)
	if err != nil {
		d := outcome.FromError(err, domain.ActionRefundOrder, req.OrderID, current.Value)
		// If approval is required, open the request so an operator can grant it.
		if d.Outcome == outcome.ApprovalRequired {
			_, _ = h.rt.RequestApproval(r.Context(), approvalID, actionCtx, contract, req.Reasoning)
		}
		writeJSON(w, statusForOutcome(d.Outcome), refundResponse(d, approvalID, nil))
		return
	}
	// Allowed: the side effect runs now, and only now.
	h.ledger.record(refundRecord{OrderID: req.OrderID, AmountCents: req.AmountCents, Reason: req.Reason, Guarded: true})
	d := outcome.Succeeded(domain.ActionRefundOrder, req.OrderID, current.Value)
	writeJSON(w, http.StatusOK, refundResponse(d, approvalID, &res))
}

// handleUnguardedRefund is the anti-pattern: it performs the business side
// effect directly, with no state check, permission, or approval. Call it twice
// and the same order gets refunded twice. This is what KIFF prevents.
func (h *demoHandler) handleUnguardedRefund(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeRefund(w, r)
	if !ok {
		return
	}
	h.ledger.record(refundRecord{OrderID: req.OrderID, AmountCents: req.AmountCents, Reason: req.Reason, Guarded: false})
	writeJSON(w, http.StatusOK, map[string]any{
		"outcome":  "executed_unguarded",
		"order_id": req.OrderID,
		"warning":  "no governance: this path will double-refund on repeat",
	})
}

func (h *demoHandler) handleLedger(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"refunds": h.ledger.all()})
}

func (h *demoHandler) handleRebuild(w http.ResponseWriter, r *http.Request) {
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

func decodeRefund(w http.ResponseWriter, r *http.Request) (refundRequest, bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return refundRequest{}, false
	}
	defer r.Body.Close()
	var req refundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return refundRequest{}, false
	}
	if req.OrderID == "" || req.AmountCents <= 0 || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "order_id, amount_cents, reason are required")
		return refundRequest{}, false
	}
	return req, true
}

func refundResponse(d outcome.Decision, approvalID string, res *action.ActionResult) map[string]any {
	body := map[string]any{
		"outcome":   d.Outcome,
		"action":    d.Action,
		"order_id":  d.EntityID,
		"state":     d.CurrentState,
		"next_step": d.NextStep,
	}
	if d.Reason != "" {
		body["reason"] = d.Reason
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

func statusForOutcome(o outcome.Outcome) int {
	switch o {
	case outcome.ApprovalRequired:
		return http.StatusAccepted
	case outcome.Blocked:
		return http.StatusConflict
	case outcome.Invalid:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
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
