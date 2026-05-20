package action

import (
	"errors"
	"testing"
)

func TestCatalogRegistersGetsAndListsContracts(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(ActionContract{Name: "PROPOSE_MOVE"}); err != nil {
		t.Fatalf("register contract: %v", err)
	}
	if err := catalog.Register(ActionContract{Name: "EXECUTE_MOVE"}); err != nil {
		t.Fatalf("register contract: %v", err)
	}

	contract, ok := catalog.Get("EXECUTE_MOVE")
	if !ok {
		t.Fatal("expected EXECUTE_MOVE contract")
	}
	if contract.Name != "EXECUTE_MOVE" {
		t.Fatalf("expected EXECUTE_MOVE, got %q", contract.Name)
	}

	contracts := catalog.List()
	if len(contracts) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(contracts))
	}
	if contracts[0].Name != "EXECUTE_MOVE" || contracts[1].Name != "PROPOSE_MOVE" {
		t.Fatalf("expected contracts sorted by name, got %#v", contracts)
	}
}

func TestCatalogRejectsEmptyAndDuplicateNames(t *testing.T) {
	catalog := NewCatalog()
	if err := catalog.Register(ActionContract{}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("expected ErrInvalidContract, got %v", err)
	}

	if err := catalog.Register(ActionContract{Name: "EXECUTE_MOVE"}); err != nil {
		t.Fatalf("register contract: %v", err)
	}
	if err := catalog.Register(ActionContract{Name: "EXECUTE_MOVE"}); !errors.Is(err, ErrDuplicateAction) {
		t.Fatalf("expected ErrDuplicateAction, got %v", err)
	}
}
