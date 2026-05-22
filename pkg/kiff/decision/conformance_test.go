package decision_test

import (
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunDecisionStore(t, func(t *testing.T) (decision.Store, func()) {
		return decision.NewInMemoryStore(), func() {}
	})
}
