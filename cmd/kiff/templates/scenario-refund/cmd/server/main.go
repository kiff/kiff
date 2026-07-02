// Command server hosts the refund scenario over HTTP.
//
// Two surfaces sit on one KIFF runtime:
//
//   - The KIFF governance API (mounted at / by httpapi) — the runtime surface.
//   - The app's own headless API (/api/...) — what an agent calls to operate
//     the app. Every /api/tools/{tool} call is validated and executed by the
//     runtime before any side effect. See api.go.
//
// Persistence has two surfaces (see README):
//   - KIFF evidence (events, decisions, approvals, audit) via -store.
//   - App state (the mock refund ledger) alongside it when persistent.
//
// -store selects the backend: memory (default off), file (JSONL under
// -data-dir), or postgres (-database-url / DATABASE_URL). With a persistent
// store the proof survives a restart: an order already REFUNDED stays refused.
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
	"path/filepath"
	"time"

	"github.com/kiff/kiff/cmd/kiff/templates/scenario-refund/domain"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/httpapi"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/store"
	filestore "github.com/kiff/kiff/pkg/kiff/store/file"
)

// postgresOpener is set by store_postgres.go (scaffolded only with
// -store=postgres). When nil, requesting -store=postgres is a clear error
// rather than a missing dependency.
var postgresOpener func(ctx context.Context, url string) (*store.Bundle, func(), error)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address; :0 picks a free port")
	storeMode := flag.String("store", "file", "store backend: memory | file | postgres")
	dataDir := flag.String("data-dir", "", "directory for file-backed stores (default ./data for -store=file)")
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "postgres connection string for -store=postgres")
	portFile := flag.String("port-file", "", "If set, write the chosen port to this file")
	flag.Parse()

	ctx := context.Background()
	rt, l, closer, err := build(ctx, *storeMode, *dataDir, *dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server failed to start: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer()
	}
	if err := seedOrders(ctx, rt); err != nil {
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
	fmt.Printf("refund scenario listening on %s (store=%s)\n", localURL(listener.Addr().String()), *storeMode)
	fmt.Println("app API:  POST /api/tools/{tool}, GET /api/actions|/api/openapi.json|/api/tools/manifest.json")
	fmt.Println("          GET  /api/entities/{id}[/timeline], POST /api/approvals/{id}/grant|deny")
	fmt.Println("NOTE: these APIs are unauthenticated. Add auth before exposing beyond localhost.")

	server := &http.Server{Handler: buildMuxWithLedger(rt, l), ReadHeaderTimeout: 5 * time.Second}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
}

// build wires the runtime and the app ledger for the chosen store backend.
func build(ctx context.Context, storeMode, dataDir, dbURL string) (*runtime.Runtime, *ledger, func(), error) {
	switch storeMode {
	case "memory":
		rt, err := domain.NewRuntime()
		return rt, newLedger(""), nil, err

	case "file":
		if dataDir == "" {
			dataDir = "./data"
		}
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, nil, nil, err
		}
		bundle, err := filestore.NewBundle(dataDir)
		if err != nil {
			return nil, nil, nil, err
		}
		sb := bundle.AsStoreBundle()
		rt, err := domain.NewRuntimeWithStores(&sb)
		if err != nil {
			_ = bundle.Close()
			return nil, nil, nil, err
		}
		return rt, newLedger(filepath.Join(dataDir, "ledger.jsonl")), func() { _ = bundle.Close() }, nil

	case "postgres":
		if postgresOpener == nil {
			return nil, nil, nil, errors.New("this build was not scaffolded with -store=postgres")
		}
		if dbURL == "" {
			return nil, nil, nil, errors.New("-store=postgres requires -database-url or DATABASE_URL")
		}
		stores, closer, err := postgresOpener(ctx, dbURL)
		if err != nil {
			return nil, nil, nil, err
		}
		rt, err := domain.NewRuntimeWithStores(stores)
		if err != nil {
			closer()
			return nil, nil, nil, err
		}
		// The mock app ledger stays in-memory here; a real app persists its
		// own business state (see README). KIFF evidence is in Postgres.
		return rt, newLedger(""), closer, nil

	default:
		return nil, nil, nil, fmt.Errorf("unknown -store %q (want memory|file|postgres)", storeMode)
	}
}

var seededOrders = []struct {
	id    string
	total int64
}{
	{"order-1", 4200},
	{"order-2", 99900},
}

// seedOrders makes each demo order exist in PAID. With a persistent store it
// is restart-safe: if the order already has events, it rehydrates the
// in-memory state from them instead of re-seeding (which would double-ingest
// or fail against the persisted state).
func seedOrders(ctx context.Context, rt *runtime.Runtime) error {
	for _, s := range seededOrders {
		if _, err := rt.RebuildState(ctx, s.id); err == nil {
			continue // restored from persisted events
		}
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

func buildMux(rt *runtime.Runtime) http.Handler {
	return buildMuxWithLedger(rt, newLedger(""))
}

// buildMuxWithLedger wires the mux against a caller-provided ledger so tests
// can inspect the recorded side effects.
func buildMuxWithLedger(rt *runtime.Runtime, l *ledger) http.Handler {
	api := newAPIHandler(rt, l)
	d := &demoHandler{rt: rt, ledger: l}

	mux := http.NewServeMux()
	api.register(mux)
	mux.HandleFunc("/demo/orders", d.handleListOrders)
	mux.HandleFunc("/demo/unguarded/refund", d.handleUnguardedRefund)
	mux.HandleFunc("/demo/ledger", d.handleLedger)
	mux.HandleFunc("/demo/rebuild", d.handleRebuild)
	mux.Handle("/", httpapi.NewHandler(rt))
	return mux
}

// demoHandler carries the /demo/* contrast routes.
type demoHandler struct {
	rt     *runtime.Runtime
	ledger *ledger
}

type refundRequest struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
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

// handleUnguardedRefund is the anti-pattern: it performs the business side
// effect directly, with no state check, permission, or approval. Call it twice
// and the same order gets refunded twice. This is what the /api path prevents.
func (h *demoHandler) handleUnguardedRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	defer r.Body.Close()
	var req refundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.OrderID == "" || req.AmountCents <= 0 || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "order_id, amount_cents, reason are required")
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

// statusForOutcome maps a decision outcome to an HTTP status for the app API.
// Convention (canonical for the generated app API): approval_required and
// blocked both map to 409 Conflict — the action cannot proceed against the
// current state — matching the core KIFF httpapi. invalid maps to 400. The
// typed `outcome` field in the body is the source of truth.
func statusForOutcome(o outcome.Outcome) int {
	switch o {
	case outcome.ApprovalRequired, outcome.Blocked:
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
