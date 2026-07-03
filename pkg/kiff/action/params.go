package action

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ParamType is the declared type of an action parameter.
type ParamType string

const (
	// ParamString is a text value. Constrain with MinLen/MaxLen, Pattern,
	// or AllowedValues.
	ParamString ParamType = "string"
	// ParamInt is an integer value. Constrain with Min/Max.
	//
	// For money, use integer minor units (e.g. amount_cents). KIFF
	// deliberately does not offer a float "decimal" type — binary floats are
	// a footgun for currency. Model money as int64 minor units.
	ParamInt ParamType = "int"
	// ParamBool is a boolean value.
	ParamBool ParamType = "bool"
	// ParamEnum is a string restricted to AllowedValues (which must be set).
	ParamEnum ParamType = "enum"
)

// ParameterSpec declares the type and constraints of a single action
// parameter. Zero-value constraint fields mean "no constraint"; pointer
// fields (Min/Max) distinguish "unset" from a real zero bound.
type ParameterSpec struct {
	Name     string
	Type     ParamType
	Required bool

	// Int constraints (inclusive). Nil means unbounded on that side.
	Min *int64
	Max *int64

	// String constraints. MinLen/MaxLen of 0 mean no bound; set MinLen=1 for
	// non-empty. Pattern is an RE2 regular expression matched against the
	// whole value when non-empty.
	MinLen  int
	MaxLen  int
	Pattern string

	// AllowedValues restricts a string/enum to this set.
	AllowedValues []string
}

// IntParam builds a required integer parameter spec (the common money/amount
// case). Use the returned value's fields to add Min/Max.
func IntParam(name string) ParameterSpec {
	return ParameterSpec{Name: name, Type: ParamInt, Required: true}
}

// StringParam builds a required, non-empty string parameter spec.
func StringParam(name string) ParameterSpec {
	return ParameterSpec{Name: name, Type: ParamString, Required: true, MinLen: 1}
}

// EnumParam builds a required enum parameter spec over the given values.
func EnumParam(name string, values ...string) ParameterSpec {
	return ParameterSpec{Name: name, Type: ParamEnum, Required: true, AllowedValues: values}
}

// validateParams checks the given parameter map against the specs. A missing
// required parameter returns ErrMissingParameter; a present-but-malformed or
// constraint-violating value returns ErrInvalidParameter. Both classify the
// action as, respectively, missing_parameter and invalid_parameter — decided
// before the executor runs.
func validateParams(specs []ParameterSpec, params map[string]any) error {
	for _, spec := range specs {
		value, present := params[spec.Name]
		if !present || value == nil {
			if spec.Required {
				return fmt.Errorf("%w: %q", ErrMissingParameter, spec.Name)
			}
			continue
		}
		if err := spec.validate(value); err != nil {
			return fmt.Errorf("%w: parameter %q: %s", ErrInvalidParameter, spec.Name, err)
		}
	}
	return nil
}

func (spec ParameterSpec) validate(value any) error {
	switch spec.Type {
	case ParamString:
		return spec.validateString(value)
	case ParamEnum:
		return spec.validateEnum(value)
	case ParamInt:
		return spec.validateInt(value)
	case ParamBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a bool, got %T", value)
		}
		return nil
	case "":
		return fmt.Errorf("parameter spec has no type")
	default:
		return fmt.Errorf("unknown parameter type %q", spec.Type)
	}
}

func (spec ParameterSpec) validateString(value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("must be a string, got %T", value)
	}
	if spec.MinLen > 0 && len(s) < spec.MinLen {
		return fmt.Errorf("must be at least %d characters", spec.MinLen)
	}
	if spec.MaxLen > 0 && len(s) > spec.MaxLen {
		return fmt.Errorf("must be at most %d characters", spec.MaxLen)
	}
	if spec.Pattern != "" {
		matched, err := regexp.MatchString(spec.Pattern, s)
		if err != nil {
			return fmt.Errorf("invalid pattern %q: %v", spec.Pattern, err)
		}
		if !matched {
			return fmt.Errorf("must match %q", spec.Pattern)
		}
	}
	if len(spec.AllowedValues) > 0 && !containsString(spec.AllowedValues, s) {
		return fmt.Errorf("must be one of %v", spec.AllowedValues)
	}
	return nil
}

func (spec ParameterSpec) validateEnum(value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("must be a string, got %T", value)
	}
	if len(spec.AllowedValues) == 0 {
		return fmt.Errorf("enum has no allowed values declared")
	}
	if !containsString(spec.AllowedValues, s) {
		return fmt.Errorf("must be one of %v", spec.AllowedValues)
	}
	return nil
}

func (spec ParameterSpec) validateInt(value any) error {
	n, ok := toInt64(value)
	if !ok {
		return fmt.Errorf("must be an integer, got %T", value)
	}
	if spec.Min != nil && n < *spec.Min {
		return fmt.Errorf("must be >= %d", *spec.Min)
	}
	if spec.Max != nil && n > *spec.Max {
		return fmt.Errorf("must be <= %d", *spec.Max)
	}
	return nil
}

// toInt64 coerces a JSON-decoded value to int64. It accepts int/int32/int64,
// an integral float64 (JSON numbers decode to float64), json.Number, and a
// numeric string (so a client that follows a string-typed API schema still
// works). Non-integral floats are rejected.
func toInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == math.Trunc(n) && !math.IsInf(n, 0) {
			return int64(n), true
		}
		return 0, false
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func containsString(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}
