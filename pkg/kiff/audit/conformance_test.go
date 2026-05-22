package audit_test

import (
	"testing"

	"github.com/kiff-framework/kiff-framework/pkg/kiff/audit"
	"github.com/kiff-framework/kiff-framework/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunAuditStore(t, func(t *testing.T) (audit.Store, func()) {
		return audit.NewInMemoryStore(), func() {}
	})
}
