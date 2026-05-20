package main

import (
	"fmt"
	"os"

	"github.com/kiff-framework/kiff-framework/examples/mission"
)

func main() {
	result, err := mission.RunHappyPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kiff demo failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("KIFF demo: mission coordination loop")
	for _, line := range result.Lines {
		fmt.Printf("- %s\n", line)
	}
	fmt.Println("Timeline:")
	for _, record := range result.Timeline {
		fmt.Printf("- %s actor=%s message=%s\n", record.Kind, record.ActorID, record.Message)
	}
	fmt.Printf("- final state: %s\n", result.FinalState.Value)
}
