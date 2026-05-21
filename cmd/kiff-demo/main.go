package main

import (
	"fmt"
	"os"

	"github.com/kiff-framework/kiff-framework/examples/mission"
)

func main() {
	// Happy path: granted approval, full loop
	happyResult, err := mission.RunHappyPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff demo happy path failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== KIFF Demo: Granted Approval (Happy Path) ===")
	for _, line := range happyResult.Lines {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println("  Timeline:")
	for _, record := range happyResult.Timeline {
		trace := record.TraceID
		if trace == "" {
			trace = "-"
		}
		fmt.Printf("    %s actor=%s trace=%s message=%s\n", record.Kind, record.ActorID, trace, record.Message)
	}
	fmt.Printf("  Final state: %s\n", happyResult.FinalState.Value)

	fmt.Println()

	// Denied path: approval denied, execution blocked
	deniedResult, err := mission.RunDeniedPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff demo denied path failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== KIFF Demo: Denied Approval (Governance Enforced) ===")
	for _, line := range deniedResult.Lines {
		fmt.Printf("  %s\n", line)
	}
	fmt.Printf("  Final state: %s\n", deniedResult.FinalState.Value)
}
