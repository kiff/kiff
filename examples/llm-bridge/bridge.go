// Package llmbridge shows how to translate an LLM-shaped tool call into a
// governed KIFF action. It is the bridge between the prompt layer (where most
// AI features start) and the KIFF layer (where governance is enforced).
//
// The bridge is intentionally tiny. It does not import an LLM SDK. The
// LLM-shaped input is just a struct with a tool name and a JSON arguments
// payload — the same shape OpenAI, Anthropic, and most agent frameworks
// converge on. Anything that produces that shape can call into KIFF.
//
// The flow:
//
//  1. The model produces a tool call.
//  2. The bridge maps the tool call to an action contract via a registered
//     translator function.
//  3. The bridge records the proposal as a KIFF decision (preserves agent
//     reasoning, evidence, confidence).
//  4. The bridge calls the runtime to validate the action.
//  5. If validation passes (and approval is satisfied), the bridge executes.
//     If not, it returns a typed error the caller can show to the model.
//
// The point: the agent's tool-call surface stays simple, but every call
// flows through the same governance gate every other actor uses.
package llmbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/actor"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/evidence"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/proposal"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
)

// ErrUnknownTool is returned when a tool call has no registered translator.
var ErrUnknownTool = errors.New("unknown tool")

// ToolCall mirrors the shape produced by every major agent framework.
//
// The Reasoning, Confidence, and Evidence fields are KIFF-flavored extras.
// They are optional in the protocol; if your agent framework does not
// surface them, leave them empty. The bridge will still work and the audit
// trail will simply have less context.
type ToolCall struct {
	// ID is a stable identifier the caller assigns. It is reused as the
	// decision id so the proposal can be correlated back to the model
	// invocation.
	ID string `json:"id"`

	// Tool is the name the model used (e.g. "refund_order"). Translators
	// map this to a KIFF action name.
	Tool string `json:"tool"`

	// Arguments is the raw JSON arguments the model produced. Translators
	// turn it into a typed parameter map.
	Arguments json.RawMessage `json:"arguments"`

	// EntityID and EntityType identify the KIFF entity the call targets.
	// Most translators read these from the arguments and override here for
	// clarity.
	EntityID   string `json:"entity_id"`
	EntityType string `json:"entity_type"`

	// CurrentState is the state the runtime should validate against. The
	// caller is responsible for resolving it (typically by reading from
	// the runtime's state machine before invoking the bridge).
	CurrentState string `json:"current_state"`

	// ApprovalID is required if the action contract requires approval.
	// The bridge does not request approval on the agent's behalf; the
	// caller decides when to escalate to a human.
	ApprovalID string `json:"approval_id,omitempty"`

	// Reasoning, Confidence, and Evidence are agent-side context that
	// gets preserved as a KIFF decision. They are how you debug an agent
	// six months from now.
	Reasoning  string         `json:"reasoning,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Evidence   []evidence.Ref `json:"evidence,omitempty"`
}

// Translator maps a tool call to a typed parameter map for a KIFF action.
// The action name is fixed by the registration; the translator only fills
// the parameters.
//
// Returning an error rejects the tool call before it reaches the runtime.
// Use this to surface argument-validation failures with model-friendly
// messages.
type Translator func(call ToolCall) (params map[string]any, err error)

// Tool is a registered tool entry: a friendly name, the KIFF action name it
// maps to, and the translator that produces parameters.
type Tool struct {
	// Name is the LLM-facing identifier. Conventionally lower_snake_case.
	Name string

	// Action is the KIFF action name the tool maps to.
	Action string

	// Translate converts arguments to KIFF parameters.
	Translate Translator
}

// Bridge owns a runtime, a registered actor identity, and the tool registry.
type Bridge struct {
	runtime *runtime.Runtime
	agent   actor.Actor
	tools   map[string]Tool
}

// NewBridge returns a bridge bound to the given runtime and agent identity.
// The agent is the actor that proposals and executions will be attributed to;
// the audit trail will show who acted.
func NewBridge(rt *runtime.Runtime, agent actor.Actor) *Bridge {
	return &Bridge{
		runtime: rt,
		agent:   agent,
		tools:   make(map[string]Tool),
	}
}

// Register adds a tool. Tools must have a non-empty Name, Action, and
// Translate.
func (b *Bridge) Register(t Tool) error {
	if t.Name == "" || t.Action == "" || t.Translate == nil {
		return fmt.Errorf("invalid tool registration: %+v", t)
	}
	b.tools[t.Name] = t
	return nil
}

// Result captures the outcome of a tool-call invocation. It is the value the
// bridge hands back to the agent's calling layer. The Outcome string is
// stable and intended for model-facing messages: "executed", "blocked",
// "approval_required", "validation_failed", "unknown_tool".
type Result struct {
	Outcome      string               `json:"outcome"`
	Action       string               `json:"action"`
	EntityID     string               `json:"entity_id"`
	Result       *action.ActionResult `json:"result,omitempty"`
	ErrorMessage string               `json:"error,omitempty"`
}

// Invoke runs the full bridge flow:
//
//  1. Look up the tool.
//  2. Translate arguments into KIFF parameters.
//  3. Record the proposal as a decision (auditable agent reasoning).
//  4. Validate the action through the runtime.
//  5. Execute if validation passes.
//
// The agent never sees the runtime directly. It only ever sees a Result.
func (b *Bridge) Invoke(ctx context.Context, call ToolCall) (Result, error) {
	tool, ok := b.tools[call.Tool]
	if !ok {
		return Result{
			Outcome:      "unknown_tool",
			Action:       call.Tool,
			ErrorMessage: ErrUnknownTool.Error(),
		}, fmt.Errorf("%w: %q", ErrUnknownTool, call.Tool)
	}

	contract, ok := b.runtime.Actions.Get(tool.Action)
	if !ok {
		return Result{
			Outcome:      "unknown_tool",
			Action:       tool.Action,
			ErrorMessage: fmt.Sprintf("action %q not registered in runtime", tool.Action),
		}, fmt.Errorf("action %q not registered in runtime", tool.Action)
	}

	params, err := tool.Translate(call)
	if err != nil {
		return Result{
			Outcome:      "validation_failed",
			Action:       tool.Action,
			EntityID:     call.EntityID,
			ErrorMessage: err.Error(),
		}, err
	}

	// Record the agent's intent as a KIFF proposal. Even if validation
	// fails next, the audit trail captures what the agent tried to do
	// and why.
	if err := b.runtime.RecordActionProposal(ctx, proposal.ActionProposal{
		ID:               proposalID(call),
		EntityID:         call.EntityID,
		EntityType:       call.EntityType,
		ActionName:       tool.Action,
		Evidence:         call.Evidence,
		ReasoningSummary: call.Reasoning,
		Confidence:       call.Confidence,
		ActorID:          b.agent.ID,
		CreatedAt:        time.Now().UTC(),
		Parameters:       params,
	}); err != nil {
		return Result{
			Outcome:      "validation_failed",
			Action:       tool.Action,
			EntityID:     call.EntityID,
			ErrorMessage: fmt.Sprintf("record proposal: %v", err),
		}, err
	}

	actionCtx := action.ActionContext{
		ActionName:   tool.Action,
		EntityID:     call.EntityID,
		EntityType:   call.EntityType,
		CurrentState: call.CurrentState,
		Actor:        b.agent,
		Parameters:   params,
		ApprovalID:   call.ApprovalID,
	}

	result, err := b.runtime.ExecuteAction(ctx, actionCtx, contract)
	if err != nil {
		outcome := classifyExecutionError(err)
		return Result{
			Outcome:      outcome,
			Action:       tool.Action,
			EntityID:     call.EntityID,
			ErrorMessage: err.Error(),
		}, err
	}

	return Result{
		Outcome:  "executed",
		Action:   tool.Action,
		EntityID: call.EntityID,
		Result:   &result,
	}, nil
}

// proposalID derives a stable proposal id from a tool call. Using the call id
// (or a derived form) keeps proposal records correlatable to the model
// invocation that produced them.
func proposalID(call ToolCall) string {
	if call.ID != "" {
		return "prop-" + call.ID
	}
	return fmt.Sprintf("prop-%d", time.Now().UnixNano())
}

// classifyExecutionError converts a runtime error into a stable outcome
// string the agent's calling layer can switch on. The outcomes are intended
// to be both stable and model-friendly: an LLM that sees "approval_required"
// can decide whether to escalate to a human, request a different action, or
// give up.
func classifyExecutionError(err error) string {
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
