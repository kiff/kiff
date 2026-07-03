package action

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kiff/kiff/pkg/kiff/permission"
)

func i64(n int64) *int64 { return &n }

// --- unit: validateParams by type and constraint ---

func TestValidateParams_MissingRequired(t *testing.T) {
	err := validateParams([]ParameterSpec{IntParam("amount_cents")}, map[string]any{})
	if !errors.Is(err, ErrMissingParameter) {
		t.Fatalf("expected ErrMissingParameter, got %v", err)
	}
}

func TestValidateParams_OptionalAbsentIsOK(t *testing.T) {
	spec := ParameterSpec{Name: "memo", Type: ParamString} // Required false
	if err := validateParams([]ParameterSpec{spec}, map[string]any{}); err != nil {
		t.Fatalf("optional absent should pass, got %v", err)
	}
}

func TestValidateParams_WrongType(t *testing.T) {
	err := validateParams([]ParameterSpec{IntParam("amount_cents")}, map[string]any{"amount_cents": true})
	if !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("expected ErrInvalidParameter, got %v", err)
	}
}

func TestValidateParams_IntConstraintsAndCoercion(t *testing.T) {
	spec := ParameterSpec{Name: "amount_cents", Type: ParamInt, Required: true, Min: i64(1), Max: i64(100000)}
	cases := []struct {
		name  string
		value any
		ok    bool
	}{
		{"int in range", 4200, true},
		{"float64 integral (JSON number)", float64(4200), true},
		{"numeric string", "4200", true},
		{"below min", 0, false},
		{"above max", 100001, false},
		{"non-integral float", 42.5, false},
		{"non-numeric string", "lots", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateParams([]ParameterSpec{spec}, map[string]any{"amount_cents": tc.value})
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidParameter) {
				t.Fatalf("expected ErrInvalidParameter, got %v", err)
			}
		})
	}
}

func TestValidateParams_String(t *testing.T) {
	spec := ParameterSpec{Name: "reason", Type: ParamString, Required: true, MinLen: 1, MaxLen: 10, Pattern: "^[a-z]+$"}
	if err := validateParams([]ParameterSpec{spec}, map[string]any{"reason": "refund"}); err != nil {
		t.Fatalf("valid string rejected: %v", err)
	}
	for _, bad := range []any{"", "WAYTOOLONGVALUE", "has spaces", 42} {
		if err := validateParams([]ParameterSpec{spec}, map[string]any{"reason": bad}); !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("expected invalid for %v, got %v", bad, err)
		}
	}
}

func TestValidateParams_Enum(t *testing.T) {
	spec := EnumParam("rail", "ach", "wire")
	if err := validateParams([]ParameterSpec{spec}, map[string]any{"rail": "ach"}); err != nil {
		t.Fatalf("valid enum rejected: %v", err)
	}
	if err := validateParams([]ParameterSpec{spec}, map[string]any{"rail": "crypto"}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("expected invalid enum, got %v", err)
	}
}

// --- integration: DefaultValidator with typed schemas + custom validator ---

func validatorContract() ActionContract {
	return ActionContract{
		Name:          "RELEASE_PAYMENT",
		AllowedStates: []string{"READY"},
		Parameters: []ParameterSpec{
			{Name: "amount_cents", Type: ParamInt, Required: true, Min: i64(1)},
			StringParam("reason"),
		},
		Risk:                RiskLow,
		ApprovalRequirement: ApprovalNever,
		Executor:            func(context.Context, ActionContext) (ActionResult, error) { return ActionResult{}, nil },
	}
}

func ctxWith(params map[string]any) ActionContext {
	return ActionContext{
		ActionName:   "RELEASE_PAYMENT",
		EntityID:     "inv-1",
		CurrentState: "READY",
		Parameters:   params,
	}
}

func TestDefaultValidator_TypedParams(t *testing.T) {
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	ctx := context.Background()

	// Valid.
	if _, err := v.Validate(ctx, ctxWith(map[string]any{"amount_cents": 4200, "reason": "eligible"}), validatorContract(), policy); err != nil {
		t.Fatalf("valid params should pass, got %v", err)
	}
	// Missing required (schema).
	if _, err := v.Validate(ctx, ctxWith(map[string]any{"reason": "eligible"}), validatorContract(), policy); !errors.Is(err, ErrMissingParameter) {
		t.Fatalf("expected missing_parameter, got %v", err)
	}
	// Malformed (constraint) — must be invalid, not blocked.
	if _, err := v.Validate(ctx, ctxWith(map[string]any{"amount_cents": 0, "reason": "eligible"}), validatorContract(), policy); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("expected invalid_parameter, got %v", err)
	}
	// Wrong type.
	if _, err := v.Validate(ctx, ctxWith(map[string]any{"amount_cents": "abc", "reason": "eligible"}), validatorContract(), policy); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("expected invalid_parameter for bad type, got %v", err)
	}
}

func TestDefaultValidator_CustomSemanticValidator(t *testing.T) {
	c := validatorContract()
	c.ValidateParameters = func(_ context.Context, actx ActionContext) error {
		amt, _ := toInt64(actx.Parameters["amount_cents"])
		if amt > 50000 {
			return errors.New("amount exceeds autonomous release threshold")
		}
		return nil
	}
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	ctx := context.Background()

	if _, err := v.Validate(ctx, ctxWith(map[string]any{"amount_cents": 4200, "reason": "ok"}), c, policy); err != nil {
		t.Fatalf("below threshold should pass, got %v", err)
	}
	_, err := v.Validate(ctx, ctxWith(map[string]any{"amount_cents": 99900, "reason": "ok"}), c, policy)
	if !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("semantic failure should classify as invalid_parameter, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "threshold") {
		t.Fatalf("expected the custom message to be preserved, got %v", err)
	}
}

// TestDefaultValidator_BackwardCompat: a contract using only RequiredParameters
// (no typed schema) behaves exactly as before.
func TestDefaultValidator_BackwardCompat(t *testing.T) {
	c := ActionContract{
		Name:                "LEGACY",
		AllowedStates:       []string{"READY"},
		RequiredParameters:  []string{"payment_id"},
		Risk:                RiskLow,
		ApprovalRequirement: ApprovalNever,
		Executor:            func(context.Context, ActionContext) (ActionResult, error) { return ActionResult{}, nil },
	}
	v := NewDefaultValidator()
	policy := permission.NewSimplePolicy()
	ctx := context.Background()
	actx := ActionContext{ActionName: "LEGACY", CurrentState: "READY", Parameters: map[string]any{"payment_id": "p1"}}
	if _, err := v.Validate(ctx, actx, c, policy); err != nil {
		t.Fatalf("legacy contract should pass, got %v", err)
	}
	actx.Parameters = map[string]any{}
	if _, err := v.Validate(ctx, actx, c, policy); !errors.Is(err, ErrMissingParameter) {
		t.Fatalf("legacy missing param should be missing_parameter, got %v", err)
	}
}
