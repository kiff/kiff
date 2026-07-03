package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

// ReviewRequirement declares the authority and segregation-of-duties
// constraints a reviewer must satisfy to review an approval.
//
// The zero value imposes no constraints, so the plain-string ReviewApproval
// path stays backward compatible. Populate it and use ReviewApprovalAs when an
// approval is part of the trust boundary — payments, refunds, shutdowns,
// access grants, data deletion — and a granted record alone is not enough.
type ReviewRequirement struct {
	// Permission, when non-empty, must be held by the reviewer. It is
	// resolved through the runtime's permission.Policy by actor ID — the same
	// trusted-membership model that backs action permissions (#19), never the
	// descriptive roles on the caller-supplied actor. A reviewer that does not
	// hold it is rejected with action.ErrPermissionDenied.
	Permission permission.Permission

	// SeparateFromRequester, when true, rejects a review whose reviewer ID
	// equals the approval's RequestedBy. The actor that proposed or requested
	// the action cannot also approve it. Rejected with approval.ErrSelfReview.
	//
	// In the KIFF flow the requesting actor is the proposer (the agent
	// proposes and the host requests approval on its behalf), so comparing
	// against RequestedBy covers the proposer-cannot-approve case.
	SeparateFromRequester bool
}

// ReviewApprovalAs grants or denies a pending approval on behalf of a reviewer,
// enforcing the given requirement before the approval changes. It verifies the
// reviewer's authority through the permission policy and, when asked, that the
// reviewer is not the actor that requested the approval. A rejected attempt
// changes nothing and is recorded as an audit.KindApprovalReviewRejected trace.
func (r *Runtime) ReviewApprovalAs(ctx context.Context, approvalID string, reviewer actor.Actor, requirement ReviewRequirement, status approval.Status, reason string) (approval.Approval, error) {
	return r.reviewApproval(ctx, approvalID, reviewer, requirement, status, reason)
}

// reviewApproval is the shared core for ReviewApproval and ReviewApprovalAs. It
// validates inputs, loads the pending approval, enforces the requirement, and
// then applies the state transition. A zero requirement skips the authority and
// segregation checks, which is what the plain-string path relies on.
func (r *Runtime) reviewApproval(ctx context.Context, approvalID string, reviewer actor.Actor, requirement ReviewRequirement, status approval.Status, reason string) (approval.Approval, error) {
	if approvalID == "" {
		return approval.Approval{}, fmt.Errorf("%w: approval id is required", approval.ErrInvalidApproval)
	}
	if reviewer.ID == "" {
		return approval.Approval{}, fmt.Errorf("%w: approval reviewed by is required", approval.ErrInvalidApproval)
	}
	if status != approval.StatusGranted && status != approval.StatusDenied {
		return approval.Approval{}, fmt.Errorf("%w: approval review status must be granted or denied", approval.ErrInvalidApproval)
	}

	existing, ok, err := r.Approvals.Get(ctx, approvalID)
	if err != nil {
		return approval.Approval{}, err
	}
	if !ok {
		return approval.Approval{}, fmt.Errorf("%w: %s", approval.ErrApprovalNotFound, approvalID)
	}
	if existing.Status != approval.StatusPending {
		return approval.Approval{}, fmt.Errorf("%w: approval %q has already been reviewed", approval.ErrInvalidApproval, approvalID)
	}

	// Authority: the reviewer must hold the required permission, resolved
	// through the policy by actor ID. Checked before the approval changes.
	if requirement.Permission != "" {
		if r.Permissions == nil || !r.Permissions.Can(ctx, reviewer, requirement.Permission) {
			r.auditReviewRejected(ctx, existing, reviewer.ID, fmt.Sprintf("reviewer lacks %q", requirement.Permission))
			return approval.Approval{}, fmt.Errorf("%w: reviewer %q lacks %q", action.ErrPermissionDenied, reviewer.ID, requirement.Permission)
		}
	}

	// Segregation of duties: the requester/proposer cannot review their own
	// approval.
	if requirement.SeparateFromRequester && existing.RequestedBy == reviewer.ID {
		r.auditReviewRejected(ctx, existing, reviewer.ID, "reviewer also requested the approval")
		return approval.Approval{}, fmt.Errorf("%w: reviewer %q also requested approval %q", approval.ErrSelfReview, reviewer.ID, approvalID)
	}

	existing.Status = status
	existing.ReviewedBy = reviewer.ID
	existing.ReviewedAt = time.Now().UTC()
	if reason != "" {
		existing.Reason = reason
	}
	if err := r.RecordApproval(ctx, existing); err != nil {
		return approval.Approval{}, err
	}
	r.metrics.Inc(CounterApprovalsReviewed, 1, EntityType(existing.EntityType))
	return existing, nil
}

// auditReviewRejected records a refused review attempt. It is best-effort: the
// rejection error returned to the caller is the authoritative result, so an
// audit write failure here does not mask why the review was refused.
func (r *Runtime) auditReviewRejected(ctx context.Context, a approval.Approval, reviewerID, reason string) {
	if r.Audit == nil {
		return
	}
	_ = r.appendAudit(ctx, audit.KindApprovalReviewRejected, a.EntityID, a.EntityType, reviewerID, "approval review rejected", map[string]any{
		"approval_id":  a.ID,
		"action":       a.ActionName,
		"requested_by": a.RequestedBy,
		"reviewer":     reviewerID,
		"reason":       reason,
	})
}
