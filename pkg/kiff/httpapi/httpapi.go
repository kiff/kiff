package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/adapter"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/permission"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/runtime"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store"
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

	ev, err := h.Runtime.IngestRaw(input)
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

	contracts, err := h.Runtime.AllowedActions(entityID)
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

	records, err := h.Runtime.Timeline(entityID)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"timeline": records,
	})
}

func entityIDFromPath(path string, suffix string) string {
	value := strings.TrimPrefix(path, "/entities/")
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
