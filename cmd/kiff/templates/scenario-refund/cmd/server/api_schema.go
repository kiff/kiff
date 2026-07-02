package main

import (
	"github.com/kiff/kiff/pkg/kiff/runtime"
)

// toolDescriptor is the compact description of one governed action, for agent
// tool runners. It is built from the runtime's action catalog so it stays in
// sync with the domain.
type toolDescriptor struct {
	Tool                string   `json:"tool"`
	Action              string   `json:"action"`
	AllowedStates       []string `json:"allowed_states,omitempty"`
	RequiredParameters  []string `json:"required_parameters,omitempty"`
	Risk                string   `json:"risk,omitempty"`
	ApprovalRequirement string   `json:"approval_requirement,omitempty"`
	Path                string   `json:"path"`
}

func toolDescriptors(rt *runtime.Runtime) []toolDescriptor {
	out := []toolDescriptor{}
	for _, c := range rt.Actions.List() {
		out = append(out, toolDescriptor{
			Tool:                toolName(c.Name),
			Action:              c.Name,
			AllowedStates:       c.AllowedStates,
			RequiredParameters:  c.RequiredParameters,
			Risk:                string(c.Risk),
			ApprovalRequirement: string(c.ApprovalRequirement),
			Path:                "/api/tools/" + toolName(c.Name),
		})
	}
	return out
}

// openAPIDoc builds a minimal OpenAPI 3 document from the action catalog: one
// POST path per tool, plus the entity read and timeline routes. It is enough
// for generic HTTP clients and tool importers to connect without hand-written
// glue, and it is generated from the same catalog as the routes.
func openAPIDoc(rt *runtime.Runtime) map[string]any {
	paths := map[string]any{}
	for _, c := range rt.Actions.List() {
		props := map[string]any{}
		required := []string{}
		for _, p := range c.RequiredParameters {
			// Limitation: action.ActionContract.RequiredParameters carries
			// names only, not types, so every parameter is typed as string
			// here. The scaffold's executors coerce numeric strings (see
			// domain.ReadIntCents), so a client following this schema still
			// works. Enriching contracts with parameter types (for an accurate
			// schema) is a framework-level follow-up.
			props[p] = map[string]any{"type": "string"}
			required = append(required, p)
		}
		paramSchema := map[string]any{"type": "object"}
		if len(props) > 0 {
			paramSchema["properties"] = props
			paramSchema["required"] = required
		}
		paths["/api/tools/"+toolName(c.Name)] = map[string]any{
			"post": map[string]any{
				"summary":     "Invoke the governed action " + c.Name,
				"operationId": toolName(c.Name),
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"entity_id":   map[string]any{"type": "string"},
									"approval_id": map[string]any{"type": "string"},
									"parameters":  paramSchema,
								},
								"required": []string{"entity_id"},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "allowed and executed"},
					"409": map[string]any{"description": "approval_required or blocked"},
					"400": map[string]any{"description": "invalid"},
				},
			},
		}
	}
	paths["/api/entities/{entityID}"] = map[string]any{
		"get": map[string]any{"summary": "Current state of an entity", "operationId": "getEntity"},
	}
	paths["/api/entities/{entityID}/timeline"] = map[string]any{
		"get": map[string]any{"summary": "Audit timeline of an entity", "operationId": "getEntityTimeline"},
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "KIFF governed app API",
			"version":     "0.1.0",
			"description": "Agent-facing tool API. Every tool call is validated and executed by the KIFF runtime before any side effect.",
		},
		"paths": paths,
	}
}
