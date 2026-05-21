package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
)

var ErrInvalidTransition = errors.New("invalid state transition")

// State is the current operational condition of an entity.
type State struct {
	EntityID   string         `json:"entity_id"`
	EntityType string         `json:"entity_type"`
	Value      string         `json:"value"`
	Version    int            `json:"version"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Transition maps an event type and source state to a target state.
type Transition struct {
	EventType string
	From      string
	To        string
}

// StateMachine applies domain-owned transition rules to KIFF state.
type StateMachine interface {
	Current(context.Context, string) (State, bool, error)
	Apply(context.Context, State, event.Event) (State, error)
	AllowedActions(context.Context, State) ([]string, error)
}

// TransitionMachine is a simple state machine for small domains and examples.
type TransitionMachine struct {
	mu             sync.RWMutex
	states         map[string]State
	transitions    []Transition
	allowedActions map[string][]string
}

// NewTransitionMachine creates an empty transition machine.
func NewTransitionMachine(transitions ...Transition) *TransitionMachine {
	return &TransitionMachine{
		states:         map[string]State{},
		transitions:    append([]Transition(nil), transitions...),
		allowedActions: map[string][]string{},
	}
}

// AddTransition registers another transition rule.
func (m *TransitionMachine) AddTransition(t Transition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transitions = append(m.transitions, t)
}

// SetAllowedActions records action names allowed for a state value.
func (m *TransitionMachine) SetAllowedActions(stateValue string, actions []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowedActions[stateValue] = append([]string(nil), actions...)
}

// Set stores a state directly. It is useful for tests and bootstrapping.
func (m *TransitionMachine) Set(st State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[st.EntityID] = st
}

// Current returns the current state for an entity.
func (m *TransitionMachine) Current(ctx context.Context, entityID string) (State, bool, error) {
	if err := ctx.Err(); err != nil {
		return State{}, false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[entityID]
	return st, ok, nil
}

// Apply applies the transition matching the event and current state.
func (m *TransitionMachine) Apply(ctx context.Context, current State, ev event.Event) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.transitions {
		if t.EventType == ev.Type && t.From == current.Value {
			next := State{
				EntityID:   ev.EntityID,
				EntityType: ev.EntityType,
				Value:      t.To,
				Version:    current.Version + 1,
				UpdatedAt:  ev.OccurredAt,
				Metadata:   map[string]any{"event_id": ev.ID, "event_type": ev.Type},
			}
			m.states[ev.EntityID] = next
			return next, nil
		}
	}

	return State{}, fmt.Errorf("%w: event %q from state %q", ErrInvalidTransition, ev.Type, current.Value)
}

// AllowedActions returns the action names configured for a state.
func (m *TransitionMachine) AllowedActions(ctx context.Context, st State) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	actions := m.allowedActions[st.Value]
	return append([]string(nil), actions...), nil
}
