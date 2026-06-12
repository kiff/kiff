package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateModulePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{"valid github path", "github.com/acme/orders", true},
		{"valid simple", "example.com/foo", true},
		{"empty", "", false},
		{"trailing slash", "github.com/acme/", false},
		{"leading slash", "/github.com/acme/orders", false},
		{"contains space", "github.com/acme orders", false},
		{"only dot", ".", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateModulePath(tc.path)
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error for %q", tc.path)
			}
		})
	}
}

func TestScaffold_ProducesRunnableProject(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "orders")

	data := templateData{
		ModulePath:  "github.com/acme/orders",
		ModuleName:  "orders",
		GoVersion:   StarterGoVersion,
		KiffVersion: StarterKiffVersion,
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	tmpl, err := resolveTemplate(templateStarter)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if err := scaffold(target, tmpl, data); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// go.mod should exist with the user's module path and not the template
	// suffix.
	goMod := readFile(t, filepath.Join(target, "go.mod"))
	if !strings.Contains(goMod, "module github.com/acme/orders") {
		t.Fatalf("go.mod missing module declaration:\n%s", goMod)
	}
	if strings.Contains(goMod, "{{") {
		t.Fatalf("go.mod still contains template markers:\n%s", goMod)
	}
	if _, err := os.Stat(filepath.Join(target, "go.mod.tmpl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected go.mod.tmpl to be renamed to go.mod, stat err: %v", err)
	}

	// Server main should exist and import the user's domain package, not the
	// in-tree template path.
	mainGo := readFile(t, filepath.Join(target, "cmd", "server", "main.go"))
	if !strings.Contains(mainGo, `"github.com/acme/orders/domain"`) {
		t.Fatalf("main.go did not rewrite domain import:\n%s", mainGo)
	}
	if strings.Contains(mainGo, starterImportPrefix) {
		t.Fatalf("main.go still references the starter import prefix:\n%s", mainGo)
	}

	// Domain file is a verbatim copy (no in-tree references to rewrite) — just
	// confirm it landed.
	domainGo := readFile(t, filepath.Join(target, "domain", "domain.go"))
	if !strings.Contains(domainGo, "package domain") {
		t.Fatalf("domain.go missing package declaration:\n%s", domainGo)
	}

	// README should have its template variables filled.
	readme := readFile(t, filepath.Join(target, "README.md"))
	if !strings.Contains(readme, "github.com/acme/orders") {
		t.Fatalf("README.md missing rendered module path:\n%s", readme)
	}
	if strings.Contains(readme, "{{") {
		t.Fatalf("README.md still contains template markers:\n%s", readme)
	}
}

func TestScaffold_ReplaceLocal(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	data := templateData{
		ModulePath:   "github.com/acme/orders",
		ModuleName:   "orders",
		GoVersion:    StarterGoVersion,
		KiffVersion:  StarterKiffVersion,
		ReplaceLocal: "../kiff-framework",
	}
	tmpl, err := resolveTemplate(templateStarter)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if err := scaffold(tmp, tmpl, data); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	goMod := readFile(t, filepath.Join(tmp, "go.mod"))
	if !strings.Contains(goMod, "replace github.com/kiff/kiff => ../kiff-framework") {
		t.Fatalf("go.mod missing replace directive:\n%s", goMod)
	}
}

func TestEnsureTargetDir(t *testing.T) {
	t.Parallel()
	t.Run("creates missing directory", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		target := filepath.Join(tmp, "fresh")
		if err := ensureTargetDir(target, false); err != nil {
			t.Fatalf("ensureTargetDir: %v", err)
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("target was not created: %v", err)
		}
	})
	t.Run("rejects non-empty directory without force", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "existing.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := ensureTargetDir(tmp, false); err == nil {
			t.Fatalf("expected error for non-empty target")
		}
	})
	t.Run("accepts non-empty directory with force", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "existing.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := ensureTargetDir(tmp, true); err != nil {
			t.Fatalf("ensureTargetDir with force: %v", err)
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}


// TestScaffold_AgenticOps_LayoutAndImports verifies the new agentic-ops
// template scaffolds with the expected files, that template variables
// are filled, and that any internal references to the embedded import
// path are rewritten to the user's module path.
func TestScaffold_AgenticOps_LayoutAndImports(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "ops")

	data := templateData{
		ModulePath:   "github.com/acme/ops",
		ModuleName:   "ops",
		GoVersion:    StarterGoVersion,
		KiffVersion:  StarterKiffVersion,
		ReplaceLocal: "../kiff-framework",
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tmpl, err := resolveTemplate(templateAgenticOps)
	if err != nil {
		t.Fatalf("resolveTemplate: %v", err)
	}
	if err := scaffold(target, tmpl, data); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	expected := []string{
		"go.mod",
		"README.md",
		"Makefile",
		"cmd/server/main.go",
		"internal/domain/refund.go",
		"internal/domain/refund_test.go",
		"agent/agent.py",
		"agent/run_no_kiff.py",
		"agent/run_with_kiff.py",
		"agent/requirements.txt",
		"agent/.env.example",
		"scripts/demo.sh",
		".gitignore",
	}
	for _, f := range expected {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Fatalf("expected %s in scaffolded project, got %v", f, err)
		}
	}

	goMod := readFile(t, filepath.Join(target, "go.mod"))
	if !strings.Contains(goMod, "module github.com/acme/ops") {
		t.Fatalf("go.mod missing module:\n%s", goMod)
	}
	if !strings.Contains(goMod, "replace github.com/kiff/kiff => ../kiff-framework") {
		t.Fatalf("go.mod missing replace directive:\n%s", goMod)
	}

	// Server main.go must import the user's domain package, not the embedded
	// template's path.
	mainGo := readFile(t, filepath.Join(target, "cmd", "server", "main.go"))
	if !strings.Contains(mainGo, `"github.com/acme/ops/internal/domain"`) {
		t.Fatalf("main.go did not rewrite domain import:\n%s", mainGo)
	}
	if strings.Contains(mainGo, agenticOpsImport) {
		t.Fatalf("main.go still references the template import prefix:\n%s", mainGo)
	}

	readme := readFile(t, filepath.Join(target, "README.md"))
	if !strings.Contains(readme, "github.com/acme/ops") {
		t.Fatalf("README missing rendered module path:\n%s", readme)
	}
	if strings.Contains(readme, "{{") {
		t.Fatalf("README still has template markers:\n%s", readme)
	}

	// .gitignore should land without the .tmpl suffix.
	if _, err := os.Stat(filepath.Join(target, ".gitignore.tmpl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".gitignore.tmpl was not stripped to .gitignore: %v", err)
	}

	// scripts/demo.sh should be executable.
	info, err := os.Stat(filepath.Join(target, "scripts", "demo.sh"))
	if err != nil {
		t.Fatalf("stat demo.sh: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("scripts/demo.sh is not executable: %v", info.Mode())
	}
}

// TestResolveTemplate covers the small switch.
func TestResolveTemplate(t *testing.T) {
	t.Parallel()
	if _, err := resolveTemplate(""); err != nil {
		t.Fatalf("default template should resolve: %v", err)
	}
	if _, err := resolveTemplate(templateStarter); err != nil {
		t.Fatalf("starter should resolve: %v", err)
	}
	if _, err := resolveTemplate(templateAgenticOps); err != nil {
		t.Fatalf("agentic-ops should resolve: %v", err)
	}
	if _, err := resolveTemplate("does-not-exist"); err == nil {
		t.Fatalf("unknown template should error")
	}
}
