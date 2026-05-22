// kiff-tour is a narrated walkthrough of the KIFF coordination loop.
//
// It is deliberately not a log dump. The output is paced, colored, and written
// in plain English so a developer can feel KIFF stop an action as clearly as
// it executes one. Run it with:
//
//	go run ./cmd/kiff-tour
//
// Pass -fast to skip the pacing if you are running it from a script or test.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kiff-framework/kiff-framework/examples/refund"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
)

// pacer controls the rhythm of the narration. It exists so the tour feels
// guided and so demos can be recorded without breakneck output.
type pacer struct {
	step  time.Duration
	beat  time.Duration
	color bool
}

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cBlue   = "\x1b[34m"
	cCyan   = "\x1b[36m"
)

func main() {
	fast := flag.Bool("fast", false, "skip pacing delays for scripted runs")
	noColor := flag.Bool("no-color", false, "disable ANSI colors")
	flag.Parse()

	p := pacer{step: 700 * time.Millisecond, beat: 200 * time.Millisecond, color: !*noColor}
	if *fast {
		p.step = 0
		p.beat = 0
	}

	if err := runTour(context.Background(), p); err != nil {
		fmt.Fprintf(os.Stderr, "tour failed: %v\n", err)
		os.Exit(1)
	}
}

func runTour(ctx context.Context, p pacer) error {
	rt, err := refund.NewRuntime()
	if err != nil {
		return fmt.Errorf("build runtime: %w", err)
	}

	p.banner("KIFF — a 90-second tour")
	p.line("We will run a tiny refund domain through the KIFF loop.")
	p.line("You will see KIFF stop an action as clearly as it executes one.")
	p.pause()

	orderID := "order-tour"
	traceID := "trace-tour-001"

	// ────────────────────────────────────────────────
	// Scene 1 — A small, low-risk action flows through.
	// ────────────────────────────────────────────────
	p.scene("Scene 1", "An order is placed and paid")
	p.line("The system places an order. KIFF records the event and moves the entity to %s.", p.bold("CREATED"))
	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-tour-1",
		Adapter:    refund.AdapterRefund,
		Type:       refund.EventOrderPlaced,
		Source:     "kiff-tour",
		EntityID:   orderID,
		EntityType: refund.EntityOrder,
		ActorID:    refund.SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
		Metadata:   event.Metadata{TraceID: traceID, CorrelationID: "corr-tour-001"},
		Payload:    map[string]any{"total": 49.0},
	}); err != nil {
		return fmt.Errorf("ingest order placed: %w", err)
	}
	p.success("event ingested  →  ORDER_PLACED")
	p.success("state           →  CREATED")
	p.pause()

	p.line("An ops agent proposes to mark the order paid. KIFF validates state, parameters, permissions.")
	markPaid, ok := rt.Actions.Get(refund.ActionMarkPaid)
	if !ok {
		return fmt.Errorf("missing %s contract", refund.ActionMarkPaid)
	}
	if _, err := rt.ExecuteAction(ctx, action.ActionContext{
		ActionName:   refund.ActionMarkPaid,
		EntityID:     orderID,
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StateCreated,
		Actor:        refund.AgentActor,
		Parameters:   map[string]any{"payment_id": "pay-9001"},
	}, markPaid); err != nil {
		return fmt.Errorf("execute mark paid: %w", err)
	}
	p.success("action executed →  MARK_PAID")
	p.success("state           →  PAID")
	p.pause()

	// ───────────────────────────────────────────────────
	// Scene 2 — A high-risk action gets stopped by KIFF.
	// ───────────────────────────────────────────────────
	p.scene("Scene 2", "An agent tries to issue a $999 refund")
	p.line("The agent confidently calls REFUND_ORDER. This is the moment that breaks unguarded systems.")
	refundContract, ok := rt.Actions.Get(refund.ActionRefundOrder)
	if !ok {
		return fmt.Errorf("missing %s contract", refund.ActionRefundOrder)
	}
	refundCtx := action.ActionContext{
		ActionName:   refund.ActionRefundOrder,
		EntityID:     orderID,
		EntityType:   refund.EntityOrder,
		CurrentState: refund.StatePaid,
		Actor:        refund.AgentActor,
		Parameters:   map[string]any{"amount": 999.0, "reason": "agent thinks customer is unhappy"},
		ApprovalID:   "approval-tour-001",
	}
	_, err = rt.ExecuteAction(ctx, refundCtx, refundContract)
	if !errors.Is(err, action.ErrApprovalRequired) {
		return fmt.Errorf("expected ErrApprovalRequired, got %v", err)
	}
	p.block("execution BLOCKED  →  REFUND_ORDER (approval required, none granted)")
	p.line("KIFF refused. The state did not change. The audit trail has the attempt.")
	p.pause()

	// ───────────────────────────────────────────────────
	// Scene 3 — The proper path: request, approve, execute.
	// ───────────────────────────────────────────────────
	p.scene("Scene 3", "A human grants approval. The same action now flows.")
	p.line("The agent records a formal approval request, including its reasoning.")
	if _, err := rt.RequestApproval(ctx, refundCtx.ApprovalID, refundCtx, refundContract, "agent-initiated refund, customer unhappy"); err != nil {
		return fmt.Errorf("request approval: %w", err)
	}
	p.info("approval requested  →  REFUND_ORDER")
	p.pause()

	p.line("An ops operator reviews and grants the approval.")
	if _, err := rt.ReviewApproval(ctx, refundCtx.ApprovalID, refund.OperatorActor.ID, approval.StatusGranted, "checked the order, refund is reasonable"); err != nil {
		return fmt.Errorf("review approval: %w", err)
	}
	p.success("approval granted    →  REFUND_ORDER (by ops-human)")
	p.pause()

	p.line("Same action context, same parameters, same agent. Now KIFF lets it through.")
	result, err := rt.ExecuteAction(ctx, refundCtx, refundContract)
	if err != nil {
		return fmt.Errorf("execute refund after grant: %w", err)
	}
	p.success("action executed →  REFUND_ORDER")
	p.success("state           →  REFUNDED")
	p.dim("    " + result.Message)
	p.pause()

	// ───────────────────────────────────────────────────
	// Scene 4 — Reconstruction. The whole story is replayable.
	// ───────────────────────────────────────────────────
	p.scene("Scene 4", "Replay and audit reconstruct the whole story")

	replay, err := rt.RebuildState(ctx, orderID)
	if err != nil {
		return fmt.Errorf("rebuild state: %w", err)
	}
	p.line("Rebuilding state from the event log alone:")
	for _, step := range replay.Steps {
		p.dim("    %s  →  %s", step.EventType, step.To)
	}
	p.success("state rebuilt   →  %s", replay.State.Value)
	p.pause()

	timeline, err := rt.Timeline(ctx, orderID)
	if err != nil {
		return fmt.Errorf("timeline: %w", err)
	}
	p.line("The audit timeline (every fact KIFF recorded for this order):")
	for _, record := range timeline {
		p.dim("    %-22s actor=%-12s %s", record.Kind, record.ActorID, summarize(record))
	}
	p.pause()

	p.banner("Done. KIFF executed the safe path, blocked the unsafe one, and explained both.")
	p.line("That is the loop: %s",
		p.bold("event → state → decision → action → approval → audit"))
	p.line("Build your domain on top of it. See %s and %s.",
		p.cyan("docs/build-a-domain.md"), p.cyan("examples/refund/"))

	return nil
}

// summarize produces a short human-readable description of an audit record
// suitable for the narrated tour.
func summarize(r audit.Record) string {
	if r.Message != "" {
		if len(r.Message) > 80 {
			return r.Message[:77] + "..."
		}
		return r.Message
	}
	return string(r.Kind)
}

// ──────────────────────────────────────────────────────────────────────────
// Pacing and formatting helpers. Kept inline to keep the tour self-contained.
// ──────────────────────────────────────────────────────────────────────────

func (p pacer) wrap(color, s string) string {
	if !p.color {
		return s
	}
	return color + s + cReset
}

func (p pacer) bold(s string) string  { return p.wrap(cBold, s) }
func (p pacer) cyan(s string) string  { return p.wrap(cCyan, s) }
func (p pacer) dim(format string, args ...any) {
	fmt.Println(p.wrap(cDim, "    "+fmt.Sprintf(format, args...)))
	p.beatPause()
}

func (p pacer) line(format string, args ...any) {
	fmt.Println("  " + fmt.Sprintf(format, args...))
	p.beatPause()
}

func (p pacer) success(format string, args ...any) {
	fmt.Println("  " + p.wrap(cGreen, "✔ "+fmt.Sprintf(format, args...)))
	p.beatPause()
}

func (p pacer) info(format string, args ...any) {
	fmt.Println("  " + p.wrap(cBlue, "• "+fmt.Sprintf(format, args...)))
	p.beatPause()
}

func (p pacer) block(format string, args ...any) {
	fmt.Println("  " + p.wrap(cRed, "✖ "+fmt.Sprintf(format, args...)))
	p.beatPause()
}

func (p pacer) scene(label, title string) {
	fmt.Println()
	fmt.Println(p.wrap(cYellow, fmt.Sprintf("── %s ── %s ──", label, title)))
	fmt.Println()
	p.beatPause()
}

func (p pacer) banner(title string) {
	bar := strings.Repeat("─", len(title)+4)
	fmt.Println()
	fmt.Println(p.wrap(cBold, bar))
	fmt.Println(p.wrap(cBold, "  "+title+"  "))
	fmt.Println(p.wrap(cBold, bar))
	fmt.Println()
	p.beatPause()
}

func (p pacer) pause() {
	if p.step > 0 {
		time.Sleep(p.step)
	}
}

func (p pacer) beatPause() {
	if p.beat > 0 {
		time.Sleep(p.beat)
	}
}
