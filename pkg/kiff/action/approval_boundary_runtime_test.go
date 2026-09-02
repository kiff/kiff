package action_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestExternalCallerCannotGrantApprovalAtRuntime is the runtime counterpart to
// TestExternalCallerCannotCompileSelfApprovalPaths.
//
// The compile-time test proves a caller cannot *name* the approved field or the
// trust.Grant type. That is a necessary property and not a sufficient one: Go's
// internal/ rule and field export rules are enforced by the compiler, and both
// reflection and unsafe operate after it. A third property — that approval
// enforcement is not delegated to the pluggable Validator — is not a language
// question at all.
//
// So each fixture here compiles and RUNS from a separate module against an
// empty approval store, and must report RESULT=refused. A fixture that prints
// RESULT=executed means an approval-required action ran with no approval record,
// which is the failure the whole boundary exists to prevent.
func TestExternalCallerCannotGrantApprovalAtRuntime(t *testing.T) {
	repoRoot := repositoryRoot(t)
	buildCache := t.TempDir()

	fixtures := []struct {
		name    string
		fixture string
		reason  string
	}{
		{
			name:    "reflection cannot mint a grant",
			fixture: "reflect_grant",
			reason:  "reflect.Zero can synthesise the un-nameable trust.Grant from the method signature",
		},
		{
			name:    "unsafe cannot set the approved bit",
			fixture: "unsafe_field",
			reason:  "unsafe can write the unexported field the compile-time boundary protects",
		},
		{
			name:    "a permissive validator cannot waive approval",
			fixture: "hostile_validator",
			reason:  "runtime.Config.ActionValidator is a public extension point",
		},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			workDir := t.TempDir()
			source, err := os.ReadFile(filepath.Join("testdata", "self_approval", tt.fixture, "main.go"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if err := os.WriteFile(filepath.Join(workDir, "main.go"), source, 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			goMod := "module example.com/kiff-boundary-runtime-test\n\n" +
				"go 1.23.0\n\n" +
				"require github.com/kiff/kiff v0.0.0\n\n" +
				"replace github.com/kiff/kiff => " + strconv.Quote(filepath.ToSlash(repoRoot)) + "\n"
			if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			// The fixture's only dependency is the framework itself, which the
			// replace directive resolves locally, so the module graph is
			// satisfied by copying the framework's own go.sum.
			if sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum")); err == nil {
				_ = os.WriteFile(filepath.Join(workDir, "go.sum"), sum, 0o600)
			}

			cmd := exec.Command("go", "run", ".")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"GOCACHE="+buildCache,
				"GOFLAGS=-mod=mod",
				"GOWORK=off",
				"GOPROXY=off",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("fixture %q failed to run: %v\n%s", tt.fixture, err, output)
			}

			got := strings.TrimSpace(string(output))
			if strings.Contains(got, "RESULT=executed") {
				t.Fatalf("SELF-APPROVAL BYPASS: %s.\n"+
					"An approval-required action executed against an empty approval store because %s.\n"+
					"output:\n%s", tt.fixture, tt.reason, output)
			}
			if !strings.Contains(got, "RESULT=refused") {
				t.Fatalf("fixture %q produced no verdict; output:\n%s", tt.fixture, output)
			}
		})
	}
}
