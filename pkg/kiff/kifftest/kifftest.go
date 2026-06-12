// Package kifftest provides small helpers for testing KIFF domains.
//
// The package follows three rules:
//
//  1. Helpers are values, not magic. Builders return real KIFF types so
//     test code remains explicit about what it is constructing.
//  2. Time is injectable. NewClock and FixedClock keep timestamps
//     deterministic without forcing the framework to depend on a clock
//     interface in production code.
//  3. Nothing here imports the runtime. These helpers are for the domain
//     side of the boundary; the runtime tests itself in pkg/kiff/runtime.
package kifftest

import (
	"sync/atomic"
	"time"

	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/permission"
)

// Default identifiers used when an EventBuilder field is omitted. Tests can
// override any of them via the With* methods.
const (
	DefaultEntityID   = "entity-test"
	DefaultEntityType = "TestEntity"
	DefaultSource     = "kifftest"
	DefaultActorID    = "actor-test"
)

// Clock is the interface domain code can accept when it wants injectable
// timestamps. Production code uses time.Now via the default clock; tests use
// FixedClock to make assertions deterministic.
type Clock interface {
	Now() time.Time
}

// systemClock returns time.Now().UTC() each call.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// NewClock returns a real-time clock suitable for production paths under
// test. Use FixedClock when you need determinism.
func NewClock() Clock { return systemClock{} }

// FixedClock returns the same instant every call until Advance or Set is used.
// Construct one with NewFixedClock.
type FixedClock struct {
	t time.Time
}

// NewFixedClock returns a fixed clock starting at the given instant. The
// instant is normalized to UTC for consistency with KIFF event timestamps.
func NewFixedClock(t time.Time) *FixedClock {
	return &FixedClock{t: t.UTC()}
}

// Now returns the clock's current instant.
func (c *FixedClock) Now() time.Time { return c.t }

// Advance moves the clock forward by d.
func (c *FixedClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// Set replaces the clock's instant with t (UTC-normalized).
func (c *FixedClock) Set(t time.Time) { c.t = t.UTC() }

// idCounter generates unique synthetic IDs for tests that do not care about
// the specific value.
var idCounter atomic.Uint64

// NextID returns a unique synthetic id with the given prefix.
func NextID(prefix string) string {
	n := idCounter.Add(1)
	return prefixedID(prefix, n)
}

// EventBuilder constructs event.Event values with sensible defaults and
// optional overrides. Use it when a test needs an event without caring about
// most fields.
//
//	ev := kifftest.NewEvent("ORDER_PLACED").
//	    WithEntityID("order-1").
//	    WithActor(kifftest.AgentActor).
//	    Build()
type EventBuilder struct {
	ev    event.Event
	clock Clock
}

// NewEvent starts an EventBuilder for the given type. Required fields default
// to package-level constants; override as needed.
func NewEvent(eventType string) *EventBuilder {
	clk := NewClock()
	return &EventBuilder{
		ev: event.Event{
			ID:         NextID("evt"),
			Type:       eventType,
			EntityID:   DefaultEntityID,
			EntityType: DefaultEntityType,
			Source:     DefaultSource,
			ActorID:    DefaultActorID,
			OccurredAt: clk.Now(),
		},
		clock: clk,
	}
}

// WithID overrides the event id.
func (b *EventBuilder) WithID(id string) *EventBuilder {
	b.ev.ID = id
	return b
}

// WithEntity sets both the entity id and entity type.
func (b *EventBuilder) WithEntity(id, entityType string) *EventBuilder {
	b.ev.EntityID = id
	b.ev.EntityType = entityType
	return b
}

// WithEntityID sets only the entity id, keeping the existing entity type.
func (b *EventBuilder) WithEntityID(id string) *EventBuilder {
	b.ev.EntityID = id
	return b
}

// WithSource overrides the event source.
func (b *EventBuilder) WithSource(s string) *EventBuilder {
	b.ev.Source = s
	return b
}

// WithActor sets the actor id from the given actor.Actor.
func (b *EventBuilder) WithActor(a actor.Actor) *EventBuilder {
	b.ev.ActorID = a.ID
	return b
}

// WithActorID sets the actor id directly.
func (b *EventBuilder) WithActorID(id string) *EventBuilder {
	b.ev.ActorID = id
	return b
}

// WithOccurredAt overrides the timestamp.
func (b *EventBuilder) WithOccurredAt(t time.Time) *EventBuilder {
	b.ev.OccurredAt = t.UTC()
	return b
}

// WithClock replaces the clock used for OccurredAt. Calling WithClock after
// WithOccurredAt is a no-op for the existing timestamp; use it before.
func (b *EventBuilder) WithClock(c Clock) *EventBuilder {
	b.clock = c
	if c != nil {
		b.ev.OccurredAt = c.Now()
	}
	return b
}

// WithTrace sets the trace and correlation ids on the event metadata.
func (b *EventBuilder) WithTrace(traceID, correlationID string) *EventBuilder {
	b.ev.Metadata.TraceID = traceID
	b.ev.Metadata.CorrelationID = correlationID
	return b
}

// WithPayload replaces the event payload.
func (b *EventBuilder) WithPayload(payload map[string]any) *EventBuilder {
	b.ev.Payload = payload
	return b
}

// Build returns the configured event.
func (b *EventBuilder) Build() event.Event {
	return b.ev
}

// Predefined actors useful in domain tests. They mirror the actors used in
// examples/refund and examples/mission so test fixtures read consistently.
var (
	SystemActor = actor.Actor{
		ID:          "test-system",
		Type:        actor.TypeSystem,
		DisplayName: "Test System",
		Roles:       []string{"system"},
	}
	AgentActor = actor.Actor{
		ID:          "test-agent",
		Type:        actor.TypeAgent,
		DisplayName: "Test Agent",
		Roles:       []string{"test_agent"},
	}
	HumanActor = actor.Actor{
		ID:          "test-human",
		Type:        actor.TypeHuman,
		DisplayName: "Test Human",
		Roles:       []string{"test_human"},
	}
)

// NewActor returns an actor.Actor with the given id and roles. Useful for
// permission-policy tests that need many distinct actors.
func NewActor(id string, roles ...string) actor.Actor {
	return actor.Actor{
		ID:    id,
		Type:  actor.TypeHuman,
		Roles: roles,
	}
}

// NewPermissionPolicy returns a *permission.SimplePolicy seeded with the
// provided role-to-permission grants. Each variadic entry is a (role,
// permission) pair.
//
//	policy := kifftest.NewPermissionPolicy(
//	    "agent",    "orders.refund",
//	    "operator", "orders.approve",
//	)
//
// Returns nil if grants are unbalanced.
func NewPermissionPolicy(grants ...string) *permission.SimplePolicy {
	if len(grants)%2 != 0 {
		return nil
	}
	p := permission.NewSimplePolicy()
	for i := 0; i < len(grants); i += 2 {
		p.GrantRole(grants[i], permission.Permission(grants[i+1]))
	}
	return p
}

// prefixedID is a small helper that avoids pulling fmt into the hot path.
func prefixedID(prefix string, n uint64) string {
	const digits = "0123456789"
	if n == 0 {
		return prefix + "-0"
	}
	// Render n in base 10 manually to avoid fmt allocation; this is a
	// hot-enough path that staying allocation-light is worth the few lines.
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return prefix + "-" + string(buf[i:])
}
