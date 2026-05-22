package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/actor"
	"github.com/kiffhq/kiff/pkg/kiff/adapter"
	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/permission"
	"github.com/kiffhq/kiff/pkg/kiff/runtime"
	"github.com/kiffhq/kiff/pkg/kiff/store"
)

// Handler exposes a small HTTP surface over a KIFF runtime.
type Handler struct {
	Runtime *runtime.Runtime
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

// NewHandler creates an HTTP handler for a runtime.
func NewHandler(rt *runtime.Runtime) *Handler {
	return &Handler{Runtime: rt}
}

// ServeHTTP routes KIFF HTTP API requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Runtime == nil {
		writeError(w, http.StatusInternalServerError, "runtime is not configured")
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
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":  true,
		"action": contract.Name,
	})
}

func (h *Handler) handleExecuteAction(w http.ResponseWriter, r *http.Request) {
	actionCtx, contract, _, ok := h.actionContextFromRequest(w, r, "/execute")
	if !ok {
		return
	}
	result, err := h.Runtime.ExecuteAction(r.Context(), actionCtx, contract)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": result,
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
	if request.Actor.ID == "" {
		writeError(w, http.StatusBadRequest, "actor id is required")
		return
	}

	reviewed, err := h.Runtime.ReviewApproval(r.Context(), approvalID, request.Actor.ID, status, request.Reason)
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
