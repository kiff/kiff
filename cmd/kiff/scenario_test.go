package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveScenarioTemplate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		scenario string
		agent    string
		want     string
		ok       bool
	}{
		{"refund default agent", "refund", "", templateScenarioRefund, true},
		{"refund custom-http", "refund", "custom-http", templateScenarioRefund, true},
		{"refund unknown agent", "refund", "agno", "", false},
		{"unknown scenario", "orders", "", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveScenarioTemplate(tc.scenario, tc.agent)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("got (%q,%v), want (%q,nil)", got, err, tc.want)
				}
			} else if err == nil {
				t.Fatalf("expected error for scenario=%q agent=%q", tc.scenario, tc.agent)
			}
		})
	}
}

// TestScaffold_ScenarioRefund_Layout scaffolds the refund scenario and checks
// the project layout, module wiring, and import rewrite.
func TestScaffold_ScenarioRefund_Layout(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "refunds")

	data := templateData{
		ModulePath:   "github.com/acme/refunds",
		ModuleName:   "refunds",
		GoVersion:    StarterGoVersion,
		KiffVersion:  StarterKiffVersion,
		ReplaceLocal: "../kiff-framework",
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tmpl, err := resolveTemplate(templateScenarioRefund)
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
		".gitignore",
		"domain/domain.go",
		"domain/domain_test.go",
		"cmd/server/main.go",
		"cmd/server/ledger.go",
		"scripts/demo.sh",
	}
	for _, f := range expected {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Fatalf("expected %s: %v", f, err)
		}
	}

	goMod := readFile(t, filepath.Join(target, "go.mod"))
	if !strings.Contains(goMod, "module github.com/acme/refunds") {
		t.Fatalf("go.mod missing module:\n%s", goMod)
	}
	if !strings.Contains(goMod, "replace github.com/kiff/kiff => ../kiff-framework") {
		t.Fatalf("go.mod missing replace directive:\n%s", goMod)
	}

	// The server must import the user's domain package, not the template path.
	mainGo := readFile(t, filepath.Join(target, "cmd", "server", "main.go"))
	if !strings.Contains(mainGo, `"github.com/acme/refunds/domain"`) {
		t.Fatalf("main.go did not rewrite domain import:\n%s", mainGo)
	}
	if strings.Contains(mainGo, scenarioRefundImport) {
		t.Fatalf("main.go still references the template import prefix")
	}

	// demo.sh should be executable.
	info, err := os.Stat(filepath.Join(target, "scripts", "demo.sh"))
	if err != nil {
		t.Fatalf("stat demo.sh: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("scripts/demo.sh is not executable: %v", info.Mode())
	}

	// README should render the module path.
	readme := readFile(t, filepath.Join(target, "README.md"))
	if !strings.Contains(readme, "github.com/acme/refunds") || strings.Contains(readme, "{{") {
		t.Fatalf("README not rendered:\n%s", readme)
	}
}

// TestRunScaffold_ScenarioViaRunNew exercises the -scenario path end to end
// through runNew (writing to a temp dir), including -agent validation.
func TestRunScaffold_ScenarioViaRunNew(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "refunds")
	if err := runNew([]string{"-scenario", "refund", "-agent", "custom-http", "-dir", target, "-force", "github.com/acme/refunds"}); err != nil {
		t.Fatalf("runNew scenario: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "domain", "domain.go")); err != nil {
		t.Fatalf("expected generated domain: %v", err)
	}
}

func TestRunScaffold_AgentRequiresScenario(t *testing.T) {
	t.Parallel()
	err := runNew([]string{"-agent", "custom-http", "github.com/acme/refunds"})
	if err == nil || !strings.Contains(err.Error(), "requires -scenario") {
		t.Fatalf("expected -agent-requires-scenario error, got %v", err)
	}
}

func TestRunScaffold_UnknownScenario(t *testing.T) {
	t.Parallel()
	err := runNew([]string{"-scenario", "nope", "github.com/acme/x"})
	if err == nil {
		t.Fatalf("expected error for unknown scenario")
	}
}

func TestResolveStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "file", true},
		{"file", "file", true},
		{"memory", "memory", true},
		{"postgres", "postgres", true},
		{"sqlite", "", false},
	}
	for _, tc := range cases {
		got, err := resolveStore(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("resolveStore(%q) = (%q,%v), want (%q,nil)", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("resolveStore(%q): expected error", tc.in)
		}
	}
}

// TestScaffold_ScenarioStoreGating: file/memory scaffolds omit the Postgres
// wiring; the postgres scaffold includes it.
func TestScaffold_ScenarioStoreGating(t *testing.T) {
	t.Parallel()
	pgFiles := []string{"docker-compose.yml", ".env.example", filepath.Join("cmd", "server", "store_postgres.go")}

	for _, store := range []string{"file", "memory"} {
		tmp := t.TempDir()
		data := templateData{ModulePath: "github.com/acme/r", ModuleName: "r", GoVersion: StarterGoVersion, KiffVersion: StarterKiffVersion, Store: store}
		tmpl, _ := resolveTemplate(templateScenarioRefund)
		if err := scaffold(tmp, tmpl, data); err != nil {
			t.Fatalf("scaffold %s: %v", store, err)
		}
		for _, f := range pgFiles {
			if _, err := os.Stat(filepath.Join(tmp, f)); !os.IsNotExist(err) {
				t.Fatalf("store=%s should not emit %s", store, f)
			}
		}
		// Makefile should carry the chosen store default.
		mk := readFile(t, filepath.Join(tmp, "Makefile"))
		if !strings.Contains(mk, "STORE ?= "+store) {
			t.Fatalf("Makefile missing STORE ?= %s:\n%s", store, mk)
		}
	}

	tmp := t.TempDir()
	data := templateData{ModulePath: "github.com/acme/r", ModuleName: "r", GoVersion: StarterGoVersion, KiffVersion: StarterKiffVersion, Store: "postgres"}
	tmpl, _ := resolveTemplate(templateScenarioRefund)
	if err := scaffold(tmp, tmpl, data); err != nil {
		t.Fatalf("scaffold postgres: %v", err)
	}
	for _, f := range pgFiles {
		if _, err := os.Stat(filepath.Join(tmp, f)); err != nil {
			t.Fatalf("store=postgres should emit %s: %v", f, err)
		}
	}
}

func TestRunScaffold_StoreRequiresScenario(t *testing.T) {
	t.Parallel()
	err := runNew([]string{"-store", "file", "github.com/acme/x"})
	if err == nil || !strings.Contains(err.Error(), "requires -scenario") {
		t.Fatalf("expected -store-requires-scenario error, got %v", err)
	}
}
