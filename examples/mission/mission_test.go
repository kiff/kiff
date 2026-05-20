package mission

import "testing"

func TestMissionHappyPathWorks(t *testing.T) {
	result, err := RunHappyPath()
	if err != nil {
		t.Fatalf("run mission happy path: %v", err)
	}
	if result.FinalState.Value != StateCompleted {
		t.Fatalf("expected final state %q, got %q", StateCompleted, result.FinalState.Value)
	}
	if len(result.Audit) == 0 {
		t.Fatal("expected audit records")
	}
}
