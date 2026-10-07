package action

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// An amount reads in its declared unit; without one it is the bare number.
func TestParameterSpec_FormatAmount(t *testing.T) {
	cents := ParameterSpec{Name: "amount_cents", Type: ParamInt, Unit: "EUR", Scale: 2}
	for _, tc := range []struct {
		spec ParameterSpec
		n    int64
		want string
	}{
		{cents, 8000, "80.00 EUR"},
		{cents, 5, "0.05 EUR"},
		{cents, -5, "-0.05 EUR"},
		{cents, 0, "0.00 EUR"},
		{cents, math.MinInt64, "-92233720368547758.08 EUR"},
		{ParameterSpec{Name: "points", Type: ParamInt, Unit: "points"}, 320, "320 points"},
		{IntParam("amount"), 8000, "8000"},
	} {
		if got := tc.spec.FormatAmount(tc.n); got != tc.want {
			t.Errorf("FormatAmount(%d) with %q/%d = %q, want %q", tc.n, tc.spec.Unit, tc.spec.Scale, got, tc.want)
		}
	}
}

// A unit that cannot describe its parameter is refused when the contract
// is registered; a contract without one registers as before.
func TestCatalog_ChecksParameterUnits(t *testing.T) {
	good := ActionContract{Name: "refund", Parameters: []ParameterSpec{{Name: "amount_cents", Type: ParamInt, Unit: "EUR", Scale: 2}}}
	if err := NewCatalog().Register(good); err != nil {
		t.Fatalf("register with a unit: %v", err)
	}
	if err := NewCatalog().Register(ActionContract{Name: "refund", Parameters: []ParameterSpec{IntParam("amount_cents")}}); err != nil {
		t.Fatalf("register without a unit: %v", err)
	}
	for _, tc := range []struct {
		name string
		spec ParameterSpec
		want string
	}{
		{"not an int", ParameterSpec{Name: "note", Type: ParamString, Unit: "EUR"}, "int only"},
		{"scale without unit", ParameterSpec{Name: "n", Type: ParamInt, Scale: 2}, "needs a unit"},
		{"not letters", ParameterSpec{Name: "n", Type: ParamInt, Unit: "€"}, "letters only"},
		{"scale too large", ParameterSpec{Name: "n", Type: ParamInt, Unit: "EUR", Scale: 7}, "0 to 6"},
		{"negative scale", ParameterSpec{Name: "n", Type: ParamInt, Unit: "EUR", Scale: -1}, "0 to 6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NewCatalog().Register(ActionContract{Name: "a", Parameters: []ParameterSpec{tc.spec}})
			if !errors.Is(err, ErrInvalidContract) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrInvalidContract saying %q", err, tc.want)
			}
		})
	}
}

// Units never change what a value must be: validation reads the integer.
func TestValidateParams_UnitIsDisplayOnly(t *testing.T) {
	spec := ParameterSpec{Name: "amount_cents", Type: ParamInt, Required: true, Unit: "EUR", Scale: 2}
	if err := validateParams([]ParameterSpec{spec}, map[string]any{"amount_cents": 8000}); err != nil {
		t.Fatalf("an integer with a unit: %v", err)
	}
	if err := validateParams([]ParameterSpec{spec}, map[string]any{"amount_cents": "80.00"}); err == nil {
		t.Fatal("a decimal string was accepted for an int with a unit")
	}
}
