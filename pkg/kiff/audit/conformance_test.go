package audit_test

import (
	"testing"

	"github.com/kiff/kiff/pkg/kiff/audit"
	"github.com/kiff/kiff/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunAuditStore(t, func(t *testing.T) (audit.Store, func()) {
		return audit.NewInMemoryStore(), func() {}
	})
}
