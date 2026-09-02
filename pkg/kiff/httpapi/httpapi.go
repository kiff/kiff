package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	"github.com/kiff/kiff/pkg/kiff/approval"
	"github.com/kiff/kiff/pkg/kiff/outcome"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/store"
)

// Handler exposes a small HTTP surface over a KIFF runtime.
type Handler struct {
	Runtime *runtime.Runtime

	// Authenticator establishes the principal for each request. When set, the
	// authenticated identity overwrites the actor in the request body before
	// any permission check or audit write, so a caller cannot act as — or
	// approve as — someone else by editing JSON.
	Authenticator Authenticator

	// AllowUnauthenticated serves requests with no Authenticator, taking the
	// actor from the request body on trust.
	//
	// This is off by default and must be set deliberately. Two legitimate
	// uses: a local demo, and a deployment where an upstream layer already
	// authenticates and rewrites the actor before delegating here (which is
	// what KIFF Cloud does). Anything else is an open door — the actor is the
	// only thing standing between a caller and approving their own action.
	AllowUnauthenticated bool
}

type actionContractResponse struct {
	Name                string                     `json:"name"`
	AllowedStates       []string                   `json:"allowed_states,omitempty"`
	RequiredParameters  []string                   `json:"required_parameters,omitempty"`
	RequiredPermissions []permission.Permission    `json:"required_permissions,omitempty"`
	Risk                action.RiskLevel           `json:"risk,omitempty"`
	ApprovalRequirement action.ApprovalRequirement `json:"approval_requirement,omitempty"`
}

type actionRequest struct {
	EntityType string         `json:"entity_type"`
	Actor      actor.Actor    `json:"actor"`
	Parameters map[string]any `json:"parameters,omitempty"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

type approvalReviewRequest struct {
	Actor  actor.Actor `json:"actor"`
	Reason string      `json:"reason,omitempty"`
}

// NewHandler creates an authenticated HTTP handler for a runtime.
//
// Every request must present a principal the Authenticator accepts; requests
// that do not are refused with 401 before any routing happens. To run without
// authentication — a local demo, or behind a layer that already authenticates
// — use NewUnauthenticatedHandler, which names what it is doing.
func NewHandler(rt *runtime.Runtime, auth Authenticator) *Handler {
	return &Handler{Runtime: rt, Authenticator: auth}
}

// NewUnauthenticatedHandler creates a handler that serves every request with
// the actor taken from the request body on trust.
//
// The name is deliberately blunt. Anyone who can reach this handler is any
// principal: they can propose an action as one identity and approve it as
// another, and the audit trail will record both as legitimate. Use it for a
// local demo, or where an upstream layer authenticates and rewrites the actor
// before delegating.
func NewUnauthenticatedHandler(rt *runtime.Runtime) *Handler {
	return &Handler{Runtime: rt, AllowUnauthenticated: true}
}

// ServeHTTP routes KIFF HTTP API requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Runtime == nil {
		writeError(w, http.StatusInternalServerError, "runtime is not configured")
		return
	}

	// Authenticate before routing. A handler with neither an Authenticator nor
	// an explicit opt-out is a misconfiguration, not an open server: refusing
	// is the only safe reading of "the operator has not decided yet".
	switch {
	case h.Authenticator != nil:
		principal, err := h.Authenticator.Authenticate(r)
		if err != nil || principal.ActorID == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		r = r.WithContext(withPrincipal(r.Context(), principal))
	case !h.AllowUnauthenticated:
		writeError(w, http.StatusInternalServerError,
			"handler has no Authenticator; construct it with NewHandler(rt, auth), "+
				"or NewUnauthenticatedHandler(rt) if this deployment authenticates upstream")
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/events/raw":
		h.handleIngestRaw(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/allowed-actions"):
		h.handleAllowedActions(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/timeline"):
		h.handleTimeline(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/validate"):
		h.handleValidateAction(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/execute"):
		h.handleExecuteAction(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/approvals"):
		h.handleRequestApproval(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/entities/") && strings.HasSuffix(r.URL.Path, "/approvals"):
		h.handleListApprovals(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/approvals/") && strings.HasSuffix(r.URL.Path, "/grant"):
		h.handleReviewApproval(w, r, approval.StatusGranted, "/grant")
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/approvals/") && strings.HasSuffix(r.URL.Path, "/deny"):
		h.handleReviewApproval(w, r, approval.StatusDenied, "/deny")
	case r.Method == http.MethodGet && r.URL.Path == "/admin":
		h.handleAdminIndex(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/":
		h.handleAdminIndex(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/entities/"):
		h.handleAdminEntity(w, r)
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
}

func (h *Handler) handleIngestRaw(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var input adapter.RawInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	// Ingestion is identity-bearing too: RawInput.ActorID lands in the event,
	// events drive state transitions, and state is what actions are judged
	// against. An unrewritten actor here would let an authenticated caller
	// attribute a state change to anyone.
	if principal, ok := PrincipalFrom(r.Context()); ok {
		input.ActorID = principal.ActorID
	}

	ev, err := h.Runtime.IngestRaw(r.Context(), input)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"event": ev,
	})
}

func (h *Handler) handleAllowedActions(w http.ResponseWriter, r *http.Request) {
	entityID := entityIDFromPath(r.URL.Path, "/allowed-actions")
	if entityID == "" {
		writeError(w, http.StatusNotFound, "entity id is required")
		return
	}

	contracts, err := h.Runtime.AllowedActions(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"actions": actionContractResponses(contracts),
	})
}

func (h *Handler) handleTimeline(w http.ResponseWriter, r *http.Request) {
	entityID := entityIDFromPath(r.URL.Path, "/timeline")
	if entityID == "" {
		writeError(w, http.StatusNotFound, "entity id is required")
		return
	}

	records, err := h.Runtime.Timeline(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"timeline": records,
	})
}

func (h *Handler) handleValidateAction(w http.ResponseWriter, r *http.Request) {
	actionCtx, contract, _, ok := h.actionContextFromRequest(w, r, "/validate")
	if !ok {
		return
	}
	if err := h.Runtime.ValidateAction(r.Context(), actionCtx, contract); err != nil {
		writeActionOutcome(w, err, actionCtx)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":   true,
		"outcome": outcome.Allowed,
		"action":  contract.Name,
	})
}

func (h *Handler) handleExecuteAction(w http.ResponseWriter, r *http.Request) {
	actionCtx, contract, _, ok := h.actionContextFromRequest(w, r, "/execute")
	if !ok {
		return
	}
	result, err := h.Runtime.ExecuteAction(r.Context(), actionCtx, contract)
	if err != nil {
		writeActionOutcome(w, err, actionCtx)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outcome": outcome.Allowed,
		"result":  result,
	})
}

func (h *Handler) handleRequestApproval(w http.ResponseWriter, r *http.Request) {
	actionCtx, contract, request, ok := h.actionContextFromRequest(w, r, "/approvals")
	if !ok {
		return
	}

	if err := h.Runtime.ValidateAction(r.Context(), actionCtx, contract); err != nil && !errors.Is(err, action.ErrApprovalRequired) {
		writeRuntimeError(w, err)
		return
	}

	requested, err := h.Runtime.RequestApproval(r.Context(), request.ApprovalID, actionCtx, contract, request.Reason)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"approval": requested,
	})
}

func (h *Handler) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	entityID := entityIDFromPath(r.URL.Path, "/approvals")
	if entityID == "" {
		writeError(w, http.StatusNotFound, "entity id is required")
		return
	}
	if h.Runtime.Approvals == nil {
		writeRuntimeError(w, store.ErrNotFound)
		return
	}
	approvals, err := h.Runtime.Approvals.List(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals": approvals,
	})
}

func (h *Handler) handleReviewApproval(w http.ResponseWriter, r *http.Request, status approval.Status, suffix string) {
	defer r.Body.Close()

	approvalID := approvalIDFromPath(r.URL.Path, suffix)
	if approvalID == "" {
		writeError(w, http.StatusNotFound, "approval id is required")
		return
	}

	var request approvalReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	reviewer := resolveActor(r.Context(), request.Actor)
	if reviewer.ID == "" {
		writeError(w, http.StatusBadRequest, "actor id is required")
		return
	}

	reviewed, err := h.Runtime.ReviewApproval(r.Context(), approvalID, reviewer.ID, status, request.Reason)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": reviewed,
	})
}

func (h *Handler) actionContextFromRequest(w http.ResponseWriter, r *http.Request, suffix string) (action.ActionContext, action.ActionContract, actionRequest, bool) {
	defer r.Body.Close()

	entityID, actionName := actionPathParts(r.URL.Path, suffix)
	if entityID == "" || actionName == "" {
		writeError(w, http.StatusNotFound, "entity id and action name are required")
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}

	var request actionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}
	// The authenticated principal replaces whatever actor the body claimed.
	request.Actor = resolveActor(r.Context(), request.Actor)
	if request.Actor.ID == "" {
		writeError(w, http.StatusBadRequest, "actor id is required")
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}

	if h.Runtime.Actions == nil {
		writeRuntimeError(w, store.ErrNotFound)
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}
	contract, ok := h.Runtime.Actions.Get(actionName)
	if !ok {
		writeError(w, http.StatusNotFound, "action contract not found")
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}

	if h.Runtime.States == nil {
		writeRuntimeError(w, store.ErrNotFound)
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}
	current, ok, err := h.Runtime.States.Current(r.Context(), entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}
	if !ok {
		writeRuntimeError(w, store.ErrNotFound)
		return action.ActionContext{}, action.ActionContract{}, actionRequest{}, false
	}

	entityType := request.EntityType
	if entityType == "" {
		entityType = current.EntityType
	}
	actionCtx := action.ActionContext{
		ActionName:   actionName,
		EntityID:     entityID,
		EntityType:   entityType,
		CurrentState: current.Value,
		Actor:        request.Actor,
		Parameters:   request.Parameters,
		ApprovalID:   request.ApprovalID,
	}
	return actionCtx, contract, request, true
}

func entityIDFromPath(path string, suffix string) string {
	value := strings.TrimPrefix(path, "/entities/")
	value = strings.TrimSuffix(value, suffix)
	return strings.Trim(value, "/")
}

func actionPathParts(path string, suffix string) (string, string) {
	value := strings.TrimPrefix(path, "/entities/")
	value = strings.TrimSuffix(value, suffix)
	value = strings.Trim(value, "/")
	parts := strings.Split(value, "/actions/")
	if len(parts) != 2 {
		return "", ""
	}
	return strings.Trim(parts[0], "/"), strings.Trim(parts[1], "/")
}

func approvalIDFromPath(path string, suffix string) string {
	value := strings.TrimPrefix(path, "/approvals/")
	value = strings.TrimSuffix(value, suffix)
	return strings.Trim(value, "/")
}

func actionContractResponses(contracts []action.ActionContract) []actionContractResponse {
	responses := make([]actionContractResponse, 0, len(contracts))
	for _, contract := range contracts {
		responses = append(responses, actionContractResponse{
			Name:                contract.Name,
			AllowedStates:       contract.AllowedStates,
			RequiredParameters:  contract.RequiredParameters,
			RequiredPermissions: contract.RequiredPermissions,
			Risk:                contract.Risk,
			ApprovalRequirement: contract.ApprovalRequirement,
		})
	}
	return responses
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, adapter.ErrInvalidRawInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, adapter.ErrAdapterNotFound):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, action.ErrPermissionDenied):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, action.ErrApprovalRequired):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, action.ErrStateNotAllowed):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, action.ErrMissingParameter):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, action.ErrExecutorMissing):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, approval.ErrInvalidApproval):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, approval.ErrApprovalNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

// actionErrorStatus maps an action decision error to its HTTP status, matching
// the status codes writeRuntimeError uses so the envelope path is consistent.
func actionErrorStatus(err error) int {
	switch {
	case errors.Is(err, action.ErrPermissionDenied):
		return http.StatusForbidden
	case errors.Is(err, action.ErrApprovalRequired):
		return http.StatusConflict
	case errors.Is(err, action.ErrStateNotAllowed):
		return http.StatusBadRequest
	case errors.Is(err, action.ErrMissingParameter):
		return http.StatusBadRequest
	case errors.Is(err, action.ErrExecutorMissing):
		return http.StatusUnprocessableEntity
	case errors.Is(err, action.ErrInvalidContract):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}

// isActionOutcome reports whether err is an action decision that maps to the
// typed outcome envelope (as opposed to a store/adapter/approval error).
func isActionOutcome(err error) bool {
	return errors.Is(err, action.ErrApprovalRequired) ||
		errors.Is(err, action.ErrPermissionDenied) ||
		errors.Is(err, action.ErrStateNotAllowed) ||
		errors.Is(err, action.ErrMissingParameter) ||
		errors.Is(err, action.ErrExecutorMissing) ||
		errors.Is(err, action.ErrInvalidContract)
}

// writeActionOutcome writes a normalized decision envelope for an action error.
// Non-action errors (store, adapter, approval) fall back to the plain error
// path so their status codes and semantics are unchanged.
func writeActionOutcome(w http.ResponseWriter, err error, actionCtx action.ActionContext) {
	if !isActionOutcome(err) {
		writeRuntimeError(w, err)
		return
	}
	d := outcome.FromError(err, actionCtx.ActionName, actionCtx.EntityID, actionCtx.CurrentState)
	writeJSON(w, actionErrorStatus(err), actionOutcomeResponse{
		Outcome:      d.Outcome,
		Reason:       d.Reason,
		Action:       d.Action,
		EntityID:     d.EntityID,
		CurrentState: d.CurrentState,
		NextStep:     d.NextStep,
		Error:        d.Message,
	})
}

// actionOutcomeResponse is the JSON body for an action decision. It carries the
// typed outcome envelope plus a backward-compatible `error` field.
type actionOutcomeResponse struct {
	Outcome      outcome.Outcome `json:"outcome"`
	Reason       outcome.Reason  `json:"reason,omitempty"`
	Action       string          `json:"action,omitempty"`
	EntityID     string          `json:"entity_id,omitempty"`
	CurrentState string          `json:"current_state,omitempty"`
	NextStep     string          `json:"next_step,omitempty"`
	Error        string          `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
