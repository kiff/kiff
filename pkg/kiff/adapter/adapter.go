package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/event"
)

var (
	ErrInvalidRawInput = errors.New("invalid raw input")
	ErrInvalidAdapter  = errors.New("invalid adapter")
	ErrAdapterNotFound = errors.New("adapter not found")
)

// RawInput is transport-neutral input waiting to become a KIFF event.
type RawInput struct {
	ID         string
	Adapter    string
	Type       string
	Source     string
	EntityID   string
	EntityType string
	ActorID    string
	ReceivedAt time.Time
	Metadata   event.Metadata
	Payload    map[string]any
}

// Validate checks the fields every adapter registration flow needs.
func (i RawInput) Validate() error {
	if i.ID == "" {
		return errors.Join(ErrInvalidRawInput, errors.New("raw input id is required"))
	}
	if i.Adapter == "" {
		return errors.Join(ErrInvalidRawInput, errors.New("raw input adapter is required"))
	}
	if i.Type == "" {
		return errors.Join(ErrInvalidRawInput, errors.New("raw input type is required"))
	}
	if i.Source == "" {
		return errors.Join(ErrInvalidRawInput, errors.New("raw input source is required"))
	}
	if i.ReceivedAt.IsZero() {
		return errors.Join(ErrInvalidRawInput, errors.New("raw input received at is required"))
	}
	return nil
}

// Adapter normalizes raw input into a KIFF event.
type Adapter interface {
	Name() string
	Normalize(context.Context, RawInput) (event.Event, error)
}

// Mapper normalizes a raw input into an event.
type Mapper func(context.Context, RawInput) (event.Event, error)

// MappingAdapter adapts a mapper function to the Adapter interface.
type MappingAdapter struct {
	name   string
	mapper Mapper
}

// NewMappingAdapter creates an adapter from a function.
func NewMappingAdapter(name string, mapper Mapper) (*MappingAdapter, error) {
	if name == "" {
		return nil, errors.Join(ErrInvalidAdapter, errors.New("adapter name is required"))
	}
	if mapper == nil {
		return nil, errors.Join(ErrInvalidAdapter, errors.New("adapter mapper is required"))
	}
	return &MappingAdapter{name: name, mapper: mapper}, nil
}

// Name returns the adapter name.
func (a *MappingAdapter) Name() string {
	return a.name
}

// Normalize maps raw input into a validated event.
func (a *MappingAdapter) Normalize(ctx context.Context, input RawInput) (event.Event, error) {
	if err := input.Validate(); err != nil {
		return event.Event{}, err
	}
	if input.Adapter != a.name {
		return event.Event{}, errors.Join(ErrInvalidRawInput, errors.New("raw input adapter does not match normalizer"))
	}
	ev, err := a.mapper(ctx, input)
	if err != nil {
		return event.Event{}, err
	}
	if err := ev.Validate(); err != nil {
		return event.Event{}, err
	}
	return ev, nil
}

// PassthroughAdapter maps structured raw input fields directly to an event.
type PassthroughAdapter struct {
	name string
}

// NewPassthroughAdapter creates an adapter for already-structured raw input.
func NewPassthroughAdapter(name string) (*PassthroughAdapter, error) {
	if name == "" {
		return nil, errors.Join(ErrInvalidAdapter, errors.New("adapter name is required"))
	}
	return &PassthroughAdapter{name: name}, nil
}

// Name returns the adapter name.
func (a *PassthroughAdapter) Name() string {
	return a.name
}

// Normalize maps raw input fields directly to an event.
func (a *PassthroughAdapter) Normalize(ctx context.Context, input RawInput) (event.Event, error) {
	if err := ctx.Err(); err != nil {
		return event.Event{}, err
	}
	if err := input.Validate(); err != nil {
		return event.Event{}, err
	}
	if input.Adapter != a.name {
		return event.Event{}, errors.Join(ErrInvalidRawInput, errors.New("raw input adapter does not match normalizer"))
	}
	ev := event.Event{
		ID:         input.ID,
		Type:       input.Type,
		EntityID:   input.EntityID,
		EntityType: input.EntityType,
		Source:     input.Source,
		ActorID:    input.ActorID,
		OccurredAt: input.ReceivedAt,
		Metadata:   input.Metadata,
		Payload:    input.Payload,
	}
	if err := ev.Validate(); err != nil {
		return event.Event{}, err
	}
	return ev, nil
}
