package domain

import (
	"errors"
	"fmt"

	"github.com/kiffhq/kiff/pkg/kiff/action"
	"github.com/kiffhq/kiff/pkg/kiff/state"
)

// Builder is a small chainable helper for assembling a Definition without
// hand-writing the state machine and action catalog wiring.
//
// The builder is a convenience layer. It does not introduce new behavior
// beyond what state.TransitionMachine and action.Catalog already provide.
type Builder struct {
	name         string
	entityTypes  []string
	eventTypes   []string
	transitions  []state.Transition
	allowed      map[string][]string
	contracts    []action.ActionContract
	machine      state.StateMachine
	customMachine bool
}

// New starts a new domain Builder with the given name.
func New(name string) *Builder {
	return &Builder{
		name:    name,
		allowed: map[string][]string{},
	}
}

// Entity declares an entity type owned by this domain.
func (b *Builder) Entity(entityType string) *Builder {
	b.entityTypes = append(b.entityTypes, entityType)
	return b
}

// Event declares an event type owned by this domain.
func (b *Builder) Event(eventType string) *Builder {
	b.eventTypes = append(b.eventTypes, eventType)
	return b
}

// Transition adds a state transition triggered by an event type.
func (b *Builder) Transition(eventType, from, to string) *Builder {
	b.transitions = append(b.transitions, state.Transition{
		EventType: eventType, From: from, To: to,
	})
	return b
}

// Allow declares which action contracts are allowed in a given state.
// Multiple Allow calls for the same state are merged.
func (b *Builder) Allow(stateValue string, actionNames ...string) *Builder {
	b.allowed[stateValue] = append(b.allowed[stateValue], actionNames...)
	return b
}

// Action registers an action contract for the domain.
func (b *Builder) Action(contract action.ActionContract) *Builder {
	b.contracts = append(b.contracts, contract)
	return b
}

// WithStateMachine lets the caller supply a custom StateMachine implementation.
// When set, Transition and Allow calls are ignored and the caller's machine is used as-is.
func (b *Builder) WithStateMachine(machine state.StateMachine) *Builder {
	b.machine = machine
	b.customMachine = true
	return b
}

// Build returns a validated Definition or an error if any contract or
// transition is malformed.
func (b *Builder) Build() (Definition, error) {
	if b.name == "" {
		return Definition{}, errors.Join(ErrInvalidDefinition, errors.New("domain name is required"))
	}

	var machine state.StateMachine
	if b.customMachine {
		machine = b.machine
	} else {
		tm := state.NewTransitionMachine(b.transitions...)
		for stateValue, actionNames := range b.allowed {
			tm.SetAllowedActions(stateValue, actionNames)
		}
		machine = tm
	}

	catalog := action.NewCatalog()
	for _, contract := range b.contracts {
		if err := catalog.Register(contract); err != nil {
			return Definition{}, fmt.Errorf("register action %q: %w", contract.Name, err)
		}
	}

	def := Definition{
		Name:         b.name,
		EntityTypes:  b.entityTypes,
		EventTypes:   b.eventTypes,
		StateMachine: machine,
		Actions:      catalog,
	}
	if err := def.Validate(); err != nil {
		return Definition{}, err
	}
	return def, nil
}
