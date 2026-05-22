package audit_test

import (
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/audit"
	"github.com/kiffhq/kiff/pkg/kiff/store/storetest"
)

func TestInMemoryStore_Conformance(t *testing.T) {
	storetest.RunAuditStore(t, func(t *testing.T) (audit.Store, func()) {
		return audit.NewInMemoryStore(), func() {}
	})
}
