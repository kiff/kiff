package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The version constants are hand-edited at release time and were missed
// through v0.9.0: the CLI installed from @latest reported 0.8.0, and a
// scaffolded project pinned v0.7.0 of the framework.
//
// The second is the one that matters. It was invisible because the
// generated code imports a package the pinned version does not have, so
// `go mod tidy` upgrades and the quickstart works — by accident. Remove
// one import and a new user silently gets a two-release-old framework.
func TestStarterPinsTheCurrentFramework(t *testing.T) {
	t.Parallel()

	if want := "v" + CLIVersion; StarterKiffVersion != want {
		t.Errorf("StarterKiffVersion = %q, CLIVersion = %q; a scaffolded project would pin %s "+
			"while the CLI that generated it is %s. Bump both at release time.",
			StarterKiffVersion, CLIVersion, StarterKiffVersion, want)
	}
}

// The README's Status line is what a reader checks the release against,
// so it must not lag the binary.
func TestReadmeStatusMatchesTheCLIVersion(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	m := regexp.MustCompile(`KIFF is at v(\d+\.\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal(`README has no "KIFF is at vX.Y" line`)
	}
	majorMinor := strings.Join(strings.Split(CLIVersion, ".")[:2], ".")
	if m[1] != majorMinor {
		t.Errorf("README says v%s, CLIVersion is %s", m[1], CLIVersion)
	}
}
