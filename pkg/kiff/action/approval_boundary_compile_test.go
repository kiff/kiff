package action_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestExternalCallerCannotCompileSelfApprovalPaths proves the public API's
// compile-time self-approval boundary from a separate module. The positive
// fixture guards the harness itself; each negative fixture must then fail for
// the boundary reason it is intended to exercise.
func TestExternalCallerCannotCompileSelfApprovalPaths(t *testing.T) {
	repoRoot := repositoryRoot(t)
	buildCache := t.TempDir()

	tests := []struct {
		name      string
		fixture   string
		wantBuild bool
		wantAny   []string
	}{
		{
			name:      "public context compiles",
			fixture:   "public_context",
			wantBuild: true,
		},
		{
			name:    "approved field is private",
			fixture: "approved_field",
			wantAny: []string{
				"cannot refer to unexported field approved",
				"unknown field approved",
			},
		},
		{
			name:    "trust grant is internal",
			fixture: "internal_grant",
			wantAny: []string{
				"use of internal package",
				"not allowed",
				"cannot import internal package",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workDir := t.TempDir()
			fixturePath := filepath.Join("testdata", "self_approval", tt.fixture, "main.go")
			source, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if err := os.WriteFile(filepath.Join(workDir, "main.go"), source, 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			goMod := "module example.com/kiff-boundary-test\n\n" +
				"go 1.23.0\n\n" +
				"require github.com/kiff/kiff v0.0.0\n\n" +
				"replace github.com/kiff/kiff => " + strconv.Quote(filepath.ToSlash(repoRoot)) + "\n"
			if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0o600); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}

			cmd := exec.Command("go", "build", ".")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"GOCACHE="+buildCache,
				"GOWORK=off",
				"GOPROXY=off",
			)
			output, err := cmd.CombinedOutput()
			if tt.wantBuild {
				if err != nil {
					t.Fatalf("public fixture should compile: %v\n%s", err, output)
				}
				return
			}

			if err == nil {
				t.Fatalf("forbidden self-approval fixture compiled successfully")
			}
			message := strings.ToLower(string(output))
			for _, want := range tt.wantAny {
				if strings.Contains(message, strings.ToLower(want)) {
					return
				}
			}
			t.Fatalf("build failed for an unexpected reason; wanted one of %q\n%s", tt.wantAny, output)
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate approval boundary test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
