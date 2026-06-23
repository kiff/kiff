package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDomainFile writes a single domain.go into a fresh temp dir and returns it.
func writeDomainFile(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "domain.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write domain.go: %v", err)
	}
	return dir
}

func hasFinding(r verifyReport, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

const completeDomain = `package domain

import (
	"context"

	"github.com/kiff/kiff/pkg/kiff/action"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

const (
	EventOrderPlaced = "ORDER_PLACED"
	EventOrderPaid   = "ORDER_PAID"

	StateCreated = "CREATED"
	StatePaid    = "PAID"

	ActionMarkPaid = "MARK_PAID"

	PermMarkPaid permission.Permission = "orders.mark_paid"
)

func markPaidContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionMarkPaid,
		AllowedStates:       []string{StateCreated},
		RequiredParameters:  []string{"payment_id"},
		RequiredPermissions: []permission.Permission{PermMarkPaid},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{ActionName: ActionMarkPaid, EntityID: ctx.EntityID, Executed: true}, nil
		},
	}
}

func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("orders")
	b = b.Event(EventOrderPlaced)
	b = b.Event(EventOrderPaid)
	b = b.Transition(EventOrderPlaced, "", StateCreated)
	b = b.Transition(EventOrderPaid, StateCreated, StatePaid)
	b = b.Allow(StateCreated, ActionMarkPaid)
	b = b.Action(markPaidContract())
	return b.Build()
}
`

func TestVerify_CompleteDomain(t *testing.T) {
	dir := writeDomainFile(t, completeDomain)
	r, err := verifyDir(dir)
	if err != nil {
		t.Fatalf("verifyDir: %v", err)
	}
	if !r.OK {
		t.Fatalf("expected OK, got findings: %+v", r.Findings)
	}
	if r.Domain != "orders" {
		t.Fatalf("expected domain name 'orders', got %q", r.Domain)
	}
}

const stubDomain = `package domain

import (
	"context"

	"github.com/kiff/kiff/pkg/kiff/action"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
)

const (
	EventOrderPlaced = "ORDER_PLACED"
	StateCreated     = "CREATED"
	ActionMarkPaid   = "MARK_PAID"
)

func markPaidContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionMarkPaid,
		AllowedStates:       []string{StateCreated},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			// TODO: implement MARK_PAID. Replace this stub with real business logic.
			return action.ActionResult{ActionName: ActionMarkPaid, EntityID: ctx.EntityID}, nil
		},
	}
}

func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("orders")
	b = b.Event(EventOrderPlaced)
	b = b.Transition(EventOrderPlaced, "", StateCreated)
	b = b.Allow(StateCreated, ActionMarkPaid)
	b = b.Action(markPaidContract())
	return b.Build()
}
`

func TestVerify_StubExecutor(t *testing.T) {
	dir := writeDomainFile(t, stubDomain)
	r, err := verifyDir(dir)
	if err != nil {
		t.Fatalf("verifyDir: %v", err)
	}
	if r.OK {
		t.Fatalf("expected failure for stub executor")
	}
	if !hasFinding(r, "executor_stub") {
		t.Fatalf("expected executor_stub finding, got: %+v", r.Findings)
	}
}

const orphanStateDomain = `package domain

import (
	"context"

	"github.com/kiff/kiff/pkg/kiff/action"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
)

const (
	EventOrderPlaced = "ORDER_PLACED"
	StateCreated     = "CREATED"
	StateShipped     = "SHIPPED"
	ActionShip       = "SHIP"
)

func shipContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionShip,
		AllowedStates:       []string{StateShipped},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{ActionName: ActionShip, EntityID: ctx.EntityID, Executed: true}, nil
		},
	}
}

func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("orders")
	b = b.Event(EventOrderPlaced)
	b = b.Transition(EventOrderPlaced, "", StateCreated)
	b = b.Allow(StateShipped, ActionShip)
	b = b.Action(shipContract())
	return b.Build()
}
`

func TestVerify_OrphanAllowedState(t *testing.T) {
	dir := writeDomainFile(t, orphanStateDomain)
	r, err := verifyDir(dir)
	if err != nil {
		t.Fatalf("verifyDir: %v", err)
	}
	if r.OK {
		t.Fatalf("expected failure for orphan allowed state")
	}
	if !hasFinding(r, "unreachable_state") {
		t.Fatalf("expected unreachable_state finding, got: %+v", r.Findings)
	}
}

const missingEventDomain = `package domain

import (
	"context"

	"github.com/kiff/kiff/pkg/kiff/action"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
)

const (
	EventOrderPlaced = "ORDER_PLACED"
	EventOrderPaid   = "ORDER_PAID"
	StateCreated     = "CREATED"
	StatePaid        = "PAID"
	ActionMarkPaid   = "MARK_PAID"
)

func markPaidContract() action.ActionContract {
	return action.ActionContract{
		Name:                ActionMarkPaid,
		AllowedStates:       []string{StateCreated},
		Risk:                action.RiskLow,
		ApprovalRequirement: action.ApprovalNever,
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			return action.ActionResult{ActionName: ActionMarkPaid, EntityID: ctx.EntityID, Executed: true}, nil
		},
	}
}

func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("orders")
	b = b.Event(EventOrderPlaced)
	// EventOrderPaid is used in a transition but never declared via Event().
	b = b.Transition(EventOrderPlaced, "", StateCreated)
	b = b.Transition(EventOrderPaid, StateCreated, StatePaid)
	b = b.Allow(StateCreated, ActionMarkPaid)
	b = b.Action(markPaidContract())
	return b.Build()
}
`

func TestVerify_MissingEvent(t *testing.T) {
	dir := writeDomainFile(t, missingEventDomain)
	r, err := verifyDir(dir)
	if err != nil {
		t.Fatalf("verifyDir: %v", err)
	}
	if r.OK {
		t.Fatalf("expected failure for undeclared event")
	}
	if !hasFinding(r, "undeclared_event") {
		t.Fatalf("expected undeclared_event finding, got: %+v", r.Findings)
	}
}

// TestVerify_OnScaffoldOutput ties #31 to #27: a freshly scaffolded domain
// carries TODO executor stubs, so verify must flag every action and fail.
func TestVerify_OnScaffoldOutput(t *testing.T) {
	d := loadDescriptor(t)
	domainGo, err := generateDomainGo(d)
	if err != nil {
		t.Fatalf("generateDomainGo: %v", err)
	}
	dir := writeDomainFile(t, string(domainGo))
	r, err := verifyDir(dir)
	if err != nil {
		t.Fatalf("verifyDir: %v", err)
	}
	if r.OK {
		t.Fatalf("scaffolded domain with TODO stubs should fail verification")
	}
	stubs := 0
	for _, f := range r.Findings {
		if f.Code == "executor_stub" {
			stubs++
		}
	}
	if stubs != len(d.Actions) {
		t.Fatalf("expected %d executor_stub findings, got %d (%+v)", len(d.Actions), stubs, r.Findings)
	}
}

func TestVerify_ResolvesDomainSubdir(t *testing.T) {
	root := t.TempDir()
	domainDir := filepath.Join(root, "domain")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "domain.go"), []byte(completeDomain), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := resolveDomainDir(root)
	if err != nil {
		t.Fatalf("resolveDomainDir: %v", err)
	}
	if got != domainDir {
		t.Fatalf("expected %s, got %s", domainDir, got)
	}
}

func TestVerifyFacts_InvalidContractFields(t *testing.T) {
	facts := domainFacts{
		Domain:         "orders",
		DeclaredEvents: map[string]bool{"E": true},
		Transitions:    []factTransition{{Event: "E", From: "", To: "S1"}},
		AllowedStates:  map[string][]string{"S1": {"A"}},
		Actions: []factAction{{
			Name:          "A",
			AllowedStates: []string{"S1"},
			HasExecutor:   true,
			// Risk and Approval intentionally empty.
		}},
	}
	r := verifyFacts(facts)
	if r.OK {
		t.Fatalf("expected failure for missing risk/approval")
	}
	if !hasFinding(r, "missing_risk") || !hasFinding(r, "missing_approval") {
		t.Fatalf("expected missing_risk and missing_approval, got: %+v", r.Findings)
	}
}
