// Package lifecycle projects an entity's governed history — proposal,
// validation, approval, execution, failure — into a single read-only view.
//
// It is a VIEW, not a new source of truth. KIFF already records the facts:
// decisions (proposals), approval records, and an append-only audit trail with
// trace/correlation metadata. In the sandbox, app code had to stitch these
// together by hand to follow an agent proposal from output to
// blocked/held/executed. This package assembles that timeline from the
// existing records so a UI or API can render it directly.
//
// It deliberately does not store raw agent output: the decision's reasoning
// summary and evidence references already carry that link, and persisting raw
// model output first-class would drift KIFF toward an LLM framework and risk
// storing PII/secrets. Raw output stays a host-owned reference on the decision.
package lifecycle

import (
	"time"

	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/decision"
)

// Stage is one governed step, projected from an audit record. The audit trail
// is the chronological spine of the lifecycle; each record becomes a stage.
type Stage struct {
	Kind          audit.Kind     `json:"kind"`
	At            time.Time      `json:"at"`
	ActorID       string         `json:"actor_id,omitempty"`
	Message       string         `json:"message,omitempty"`
	Action        string         `json:"action,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

// Lifecycle is a read-only projection of an entity's governed history. Stages
// are ordered as the audit store returns them (chronological). Decisions and
// Approvals are the detail records attached for reference.
type Lifecycle struct {
	EntityID     string              `json:"entity_id"`
	EntityType   string              `json:"entity_type,omitempty"`
	CurrentState string              `json:"current_state,omitempty"`
	Stages       []Stage             `json:"stages"`
	Decisions    []decision.Decision `json:"decisions,omitempty"`
	Approvals    []approval.Approval `json:"approvals,omitempty"`
}

// Assemble projects the given records into a lifecycle view. Audit records are
// the chronological spine (callers should pass them in order); decisions and
// approvals are attached for detail. CurrentState is derived from the most
// recent state-change stage.
func Assemble(entityID string, records []audit.Record, decisions []decision.Decision, approvals []approval.Approval) Lifecycle {
	lc := Lifecycle{
		EntityID:  entityID,
		Decisions: decisions,
		Approvals: approvals,
		Stages:    make([]Stage, 0, len(records)),
	}
	for _, r := range records {
		if lc.EntityType == "" {
			lc.EntityType = r.EntityType
		}
		if r.Kind == audit.KindStateChanged {
			if to, ok := r.Data["to"].(string); ok && to != "" {
				lc.CurrentState = to
			}
		}
		action, _ := r.Data["action"].(string)
		lc.Stages = append(lc.Stages, Stage{
			Kind:          r.Kind,
			At:            r.CreatedAt,
			ActorID:       r.ActorID,
			Message:       r.Message,
			Action:        action,
			CorrelationID: r.CorrelationID,
			Data:          r.Data,
		})
	}
	return lc
}

// Has reports whether any stage of the given kind is present anywhere in the
// history (useful for "did this entity ever…" questions).
func (l Lifecycle) Has(kind audit.Kind) bool {
	for _, s := range l.Stages {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

// LastStage returns the most recent stage, if any.
func (l Lifecycle) LastStage() (Stage, bool) {
	if len(l.Stages) == 0 {
		return Stage{}, false
	}
	return l.Stages[len(l.Stages)-1], true
}

// dispositionKinds are the stages that represent the outcome of an action
// attempt. The most recent of these is the entity's current governed status.
func isDisposition(kind audit.Kind) bool {
	switch kind {
	case audit.KindActionExecuted, audit.KindActionFailed,
		audit.KindApprovalRequired, audit.KindApprovalGranted,
		audit.KindApprovalDenied, audit.KindApprovalReviewRejected,
		audit.KindActionDeduplicated:
		return true
	}
	return false
}

// Disposition returns the kind of the most recent action-outcome stage — the
// entity's current governed status. An entity may pass through several actions
// over its life; Disposition reflects the latest attempt, not the whole
// history. It is empty when no action has been attempted yet.
func (l Lifecycle) Disposition() audit.Kind {
	for i := len(l.Stages) - 1; i >= 0; i-- {
		if isDisposition(l.Stages[i].Kind) {
			return l.Stages[i].Kind
		}
	}
	return ""
}

// Executed reports whether the latest action attempt executed.
func (l Lifecycle) Executed() bool { return l.Disposition() == audit.KindActionExecuted }

// Failed reports whether the latest action attempt was blocked or failed
// validation/execution.
func (l Lifecycle) Failed() bool { return l.Disposition() == audit.KindActionFailed }

// Denied reports whether the latest action attempt's approval was denied.
func (l Lifecycle) Denied() bool { return l.Disposition() == audit.KindApprovalDenied }

// AwaitingApproval reports whether the latest action attempt is held waiting on
// a human approval that has not yet been granted, denied, or executed.
func (l Lifecycle) AwaitingApproval() bool {
	return l.Disposition() == audit.KindApprovalRequired
}
