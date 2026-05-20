package domain

import (
	"errors"
	"slices"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/action"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/state"
)

var ErrInvalidDefinition = errors.New("invalid domain definition")

// Definition bundles the domain-owned coordination pieces KIFF needs at runtime.
type Definition struct {
	Name         string
	EntityTypes  []string
	EventTypes   []string
	StateMachine state.StateMachine
	Actions      *action.Catalog
}

// Validate checks that the domain has the minimum wiring needed by KIFF.
func (d Definition) Validate() error {
	if d.Name == "" {
		return errors.Join(ErrInvalidDefinition, errors.New("domain name is required"))
	}
	if d.StateMachine == nil {
		return errors.Join(ErrInvalidDefinition, errors.New("domain state machine is required"))
	}
	if d.Actions == nil {
		return errors.Join(ErrInvalidDefinition, errors.New("domain action catalog is required"))
	}
	return nil
}

// KnowsEntityType returns true when the domain declares the entity type.
func (d Definition) KnowsEntityType(entityType string) bool {
	return slices.Contains(d.EntityTypes, entityType)
}

// KnowsEventType returns true when the domain declares the event type.
func (d Definition) KnowsEventType(eventType string) bool {
	return slices.Contains(d.EventTypes, eventType)
}
