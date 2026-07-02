package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// TestTour_RunsAndShowsKeyMoments verifies the narrated tour completes without
// error and that the load-bearing beats appear in the output: a paid state
// transition, the agent executing a low-risk action, a high-risk action held
// for approval, and a granted approval that succeeds.
func TestTour_RunsAndShowsKeyMoments(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runTour(context.Background(), pacer{step: 0, beat: 0, color: false}); err != nil {
			t.Fatalf("runTour: %v", err)
		}
	})

	required := []string{
		"state           →  PAID",
		"execution HELD",
		"approval granted",
		"state           →  REFUNDED",
		"state rebuilt",
	}
	for _, marker := range required {
		if !strings.Contains(out, marker) {
			t.Fatalf("tour output missing %q\n----\n%s\n----", marker, out)
		}
	}
}

// captureStdout runs fn while redirecting os.Stdout to a pipe and returns the
// captured output. Used to assert the narration without exposing internal
// formatting helpers in the production binary.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()

	fn()

	_ = w.Close()
	os.Stdout = orig
	return <-done
}
