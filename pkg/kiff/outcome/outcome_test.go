package outcome

import (
	"errors"
	"fmt"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		outcome Outcome
		reason  Reason
	}{
		{"nil is allowed", nil, Allowed, ReasonNone},
		{"approval required", action.ErrApprovalRequired, ApprovalRequired, ReasonApprovalRequired},
		{"state not allowed -> blocked", action.ErrStateNotAllowed, Blocked, ReasonStateNotAllowed},
		{"permission denied -> blocked", action.ErrPermissionDenied, Blocked, ReasonPermissionDenied},
		{"missing parameter -> invalid", action.ErrMissingParameter, Invalid, ReasonMissingParameter},
		{"invalid parameter -> invalid", action.ErrInvalidParameter, Invalid, ReasonInvalidParameter},
		{"executor missing -> invalid", action.ErrExecutorMissing, Invalid, ReasonExecutorMissing},
		{"invalid contract -> invalid", action.ErrInvalidContract, Invalid, ReasonInvalidContract},
		{"unknown error fails safe to blocked", errors.New("boom"), Blocked, ReasonError},
		{"wrapped sentinel still classifies", fmt.Errorf("wrap: %w", action.ErrStateNotAllowed), Blocked, ReasonStateNotAllowed},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			oc, reason := Classify(tc.err)
			if oc != tc.outcome || reason != tc.reason {
				t.Fatalf("Classify(%v) = (%q, %q), want (%q, %q)", tc.err, oc, reason, tc.outcome, tc.reason)
			}
		})
	}
}

func TestFromError_ApprovalSetsNextStep(t *testing.T) {
	d := FromError(action.ErrApprovalRequired, "REFUND_ORDER", "order-1", "PAID")
	if d.Outcome != ApprovalRequired {
		t.Fatalf("outcome = %q", d.Outcome)
	}
	if d.Reason != ReasonApprovalRequired {
		t.Fatalf("reason = %q", d.Reason)
	}
	if d.NextStep != NextRequestApproval {
		t.Fatalf("next_step = %q, want %q", d.NextStep, NextRequestApproval)
	}
	if d.Action != "REFUND_ORDER" || d.EntityID != "order-1" || d.CurrentState != "PAID" {
		t.Fatalf("envelope fields not populated: %+v", d)
	}
	if d.Message == "" {
		t.Fatalf("expected a message from the error")
	}
	if d.OK() {
		t.Fatalf("approval_required must not be OK")
	}
}

func TestFromError_BlockedHasNoNextStep(t *testing.T) {
	d := FromError(action.ErrStateNotAllowed, "MARK_PAID", "order-1", "PAID")
	if d.Outcome != Blocked || d.Reason != ReasonStateNotAllowed {
		t.Fatalf("unexpected: %+v", d)
	}
	if d.NextStep != "" {
		t.Fatalf("blocked should not suggest a next step, got %q", d.NextStep)
	}
}

func TestSucceeded(t *testing.T) {
	d := Succeeded("MARK_PAID", "order-1", "CREATED")
	if !d.OK() || d.Outcome != Allowed {
		t.Fatalf("expected allowed/OK, got %+v", d)
	}
	if d.Reason != ReasonNone {
		t.Fatalf("allowed should have no reason, got %q", d.Reason)
	}
}

func TestUnknownAction(t *testing.T) {
	d := UnknownAction("delete_universe", "order-1")
	if d.Outcome != Invalid || d.Reason != ReasonUnknownAction {
		t.Fatalf("unexpected: %+v", d)
	}
}
