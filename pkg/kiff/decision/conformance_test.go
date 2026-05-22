package decision_test

import (
	"testing"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/decision"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunDecisionStore(t, func(t *testing.T) (decision.Store, func()) {
		return decision.NewInMemoryStore(), func() {}
	})
}
