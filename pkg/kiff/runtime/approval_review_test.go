package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

const reviewPermission permission.Permission = "payment.review_release"

// pendingRelease requests a pending approval for a release-style action,
// requested by the given actor id, and returns the runtime.
func pendingRelease(t *testing.T, rt *Runtime, approvalID, requestedBy string) {
	t.Helper()
	actionCtx := action.ActionContext{
		ActionName: "RELEASE_PAYMENT", EntityID: "inv-1", EntityType: "Invoice",
		Actor: actor.Actor{ID: requestedBy},
	}
	contract := action.ActionContract{Name: "RELEASE_PAYMENT", ApprovalRequirement: action.ApprovalRequired}
	if _, err := rt.RequestApproval(context.Background(), approvalID, actionCtx, contract, "needs authority"); err != nil {
		t.Fatalf("request approval: %v", err)
	}
}

func TestReviewApprovalAsAllowsAuthorizedReviewer(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("reviewer", reviewPermission)
	rt := mustNew(t, Config{PermissionPolicy: policy})
	pendingRelease(t, rt, "ap-1", "agent")

	reviewer := actor.Actor{ID: "reviewer", Type: actor.TypeHuman}
	req := ReviewRequirement{Permission: reviewPermission, SeparateFromRequester: true}
	got, err := rt.ReviewApprovalAs(context.Background(), "ap-1", reviewer, req, approval.StatusGranted, "approved")
	if err != nil {
		t.Fatalf("authorized reviewer should succeed, got %v", err)
	}
	if got.Status != approval.StatusGranted || got.ReviewedBy != "reviewer" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestReviewApprovalAsRejectsUnauthorizedReviewer(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("reviewer", reviewPermission)
	rt := mustNew(t, Config{PermissionPolicy: policy})
	pendingRelease(t, rt, "ap-1", "agent")

	// A reviewer that does not hold the reviewer permission.
	intruder := actor.Actor{ID: "someone-else", Type: actor.TypeHuman}
	req := ReviewRequirement{Permission: reviewPermission}
	_, err := rt.ReviewApprovalAs(context.Background(), "ap-1", intruder, req, approval.StatusGranted, "sneaky")
	if !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	// The approval must remain pending — a rejected review changes nothing.
	stored, ok, _ := rt.Approvals.Get(context.Background(), "ap-1")
	if !ok || stored.Status != approval.StatusPending {
		t.Fatalf("approval should still be pending, got %+v", stored)
	}
}

func TestReviewApprovalAsRejectsServiceActorWithoutReviewerPermission(t *testing.T) {
	// Segregation modeled through permissions, not actor-type special-casing:
	// the executing service actor simply does not hold the reviewer permission.
	policy := permission.NewSimplePolicy()
	policy.GrantActor("reviewer", reviewPermission)
	rt := mustNew(t, Config{PermissionPolicy: policy})
	pendingRelease(t, rt, "ap-1", "agent")

	service := actor.Actor{ID: "payment-service", Type: actor.TypeService}
	req := ReviewRequirement{Permission: reviewPermission}
	if _, err := rt.ReviewApprovalAs(context.Background(), "ap-1", service, req, approval.StatusGranted, "self-serve"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected service actor review to be denied, got %v", err)
	}
}

func TestReviewApprovalAsRejectsSelfReview(t *testing.T) {
	policy := permission.NewSimplePolicy()
	// The requester happens to also hold the reviewer permission, but SoD
	// must still stop them approving their own request.
	policy.GrantActor("agent", reviewPermission)
	rt := mustNew(t, Config{PermissionPolicy: policy})
	pendingRelease(t, rt, "ap-1", "agent")

	self := actor.Actor{ID: "agent", Type: actor.TypeAgent}
	req := ReviewRequirement{Permission: reviewPermission, SeparateFromRequester: true}
	_, err := rt.ReviewApprovalAs(context.Background(), "ap-1", self, req, approval.StatusGranted, "self approve")
	if !errors.Is(err, approval.ErrSelfReview) {
		t.Fatalf("expected ErrSelfReview, got %v", err)
	}
	stored, ok, _ := rt.Approvals.Get(context.Background(), "ap-1")
	if !ok || stored.Status != approval.StatusPending {
		t.Fatalf("approval should still be pending, got %+v", stored)
	}
}

func TestReviewApprovalAsRejectedAttemptIsAudited(t *testing.T) {
	policy := permission.NewSimplePolicy()
	policy.GrantActor("reviewer", reviewPermission)
	rt := mustNew(t, Config{PermissionPolicy: policy})
	pendingRelease(t, rt, "ap-1", "agent")

	intruder := actor.Actor{ID: "intruder", Type: actor.TypeHuman}
	req := ReviewRequirement{Permission: reviewPermission}
	_, _ = rt.ReviewApprovalAs(context.Background(), "ap-1", intruder, req, approval.StatusGranted, "nope")

	records, err := rt.Timeline(context.Background(), "inv-1")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	var sawRejected bool
	for _, rec := range records {
		if rec.Kind == audit.KindApprovalReviewRejected && rec.ActorID == "intruder" {
			sawRejected = true
		}
	}
	if !sawRejected {
		t.Fatal("expected an approval_review_rejected audit record for the intruder")
	}
}

func TestReviewApprovalAsRequiresPolicyWhenPermissionDeclared(t *testing.T) {
	// No policy configured but a reviewer permission is required: fail closed.
	rt := mustNew(t, Config{})
	pendingRelease(t, rt, "ap-1", "agent")
	req := ReviewRequirement{Permission: reviewPermission}
	if _, err := rt.ReviewApprovalAs(context.Background(), "ap-1", actor.Actor{ID: "reviewer"}, req, approval.StatusGranted, "x"); !errors.Is(err, action.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied with no policy, got %v", err)
	}
}

// TestReviewApprovalBackwardCompatNoRequirement: the plain-string path keeps
// working with no policy and no separation check (simple demos).
func TestReviewApprovalBackwardCompatNoRequirement(t *testing.T) {
	rt := mustNew(t, Config{})
	pendingRelease(t, rt, "ap-1", "agent")
	got, err := rt.ReviewApproval(context.Background(), "ap-1", "whoever", approval.StatusGranted, "ok")
	if err != nil {
		t.Fatalf("plain review should succeed, got %v", err)
	}
	if got.Status != approval.StatusGranted || got.ReviewedBy != "whoever" {
		t.Fatalf("unexpected result: %+v", got)
	}
}
