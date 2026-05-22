package file_test

import (
	"path/filepath"
	"testing"

	"github.com/kiffhq/kiff/pkg/kiff/approval"
	"github.com/kiffhq/kiff/pkg/kiff/audit"
	"github.com/kiffhq/kiff/pkg/kiff/decision"
	"github.com/kiffhq/kiff/pkg/kiff/event"
	"github.com/kiffhq/kiff/pkg/kiff/store/file"
	"github.com/kiffhq/kiff/pkg/kiff/store/storetest"
)

// Each conformance run gets its own bundle in a fresh temp directory so the
// stores are isolated between subtests.

func TestEventStore_Conformance(t *testing.T) {
	storetest.RunEventStore(t, func(t *testing.T) (event.Store, func()) {
		bundle := newBundle(t)
		return bundle.Events, func() { _ = bundle.Close() }
	})
}

func TestDecisionStore_Conformance(t *testing.T) {
	storetest.RunDecisionStore(t, func(t *testing.T) (decision.Store, func()) {
		bundle := newBundle(t)
		return bundle.Decisions, func() { _ = bundle.Close() }
	})
}

func TestApprovalStore_Conformance(t *testing.T) {
	storetest.RunApprovalStore(t, func(t *testing.T) (approval.Store, func()) {
		bundle := newBundle(t)
		return bundle.Approvals, func() { _ = bundle.Close() }
	})
}

func TestAuditStore_Conformance(t *testing.T) {
	storetest.RunAuditStore(t, func(t *testing.T) (audit.Store, func()) {
		bundle := newBundle(t)
		return bundle.Audit, func() { _ = bundle.Close() }
	})
}

// newBundle returns a fresh file-backed store bundle rooted in t.TempDir().
func newBundle(t *testing.T) *file.Bundle {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	bundle, err := file.NewBundle(dir)
	if err != nil {
		t.Fatalf("file.NewBundle: %v", err)
	}
	return bundle
}
