package event_test

import (
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/store/storetest"
)

// TestInMemoryStore_Conformance runs the shared store conformance suite
// against the in-memory event store. The same suite runs against every
// implementation (file, postgres, sqlite, dynamodb) to guarantee they all
// behave the same way.
func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunEventStore(t, func(t *testing.T) (event.Store, func()) {
		return event.NewInMemoryStore(), func() {}
	})
}
