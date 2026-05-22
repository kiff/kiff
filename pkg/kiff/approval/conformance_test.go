package approval_test

import (
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunApprovalStore(t, func(t *testing.T) (approval.Store, func()) {
		return approval.NewInMemoryStore(), func() {}
	})
}
