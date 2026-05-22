package approval_test

import (
	"testing"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/approval"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunApprovalStore(t, func(t *testing.T) (approval.Store, func()) {
		return approval.NewInMemoryStore(), func() {}
	})
}
