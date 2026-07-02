package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

// starterFS embeds the starter project layout. The embedded path mirrors the
// in-tree layout under cmd/kiff/templates/starter so the same files are both
// the live reference (compiled with the framework) and the scaffold source.
//
//go:embed all:templates/starter
var starterFS embed.FS

// agenticOpsFS embeds the agentic-ops template tree.
//
//go:embed all:templates/agentic-ops
var agenticOpsFS embed.FS

// scenarioRefundFS embeds the refund governed-action scenario template.
//
//go:embed all:templates/scenario-refund
var scenarioRefundFS embed.FS

const (
	templateStarter        = "starter"
	templateAgenticOps     = "agentic-ops"
	templateScenarioRefund = "scenario-refund"
	starterRoot            = "templates/starter"
	agenticOpsRoot         = "templates/agentic-ops"
	scenarioRefundRoot     = "templates/scenario-refund"
	starterImportPrefix    = "github.com/kiff/kiff/cmd/kiff/templates/starter"
	agenticOpsImport       = "github.com/kiff/kiff/cmd/kiff/templates/agentic-ops"
	scenarioRefundImport   = "github.com/kiff/kiff/cmd/kiff/templates/scenario-refund"
	goModTemplateName      = "go.mod.tmpl"
)

// templateData feeds text/template rendering for files like go.mod.tmpl and
// README.md.
type templateData struct {
	ModulePath   string // full module path, e.g. github.com/acme/orders
	ModuleName   string // last path segment, e.g. orders
	GoVersion    string
	KiffVersion  string
	ReplaceLocal string // optional path used to emit a `replace` directive
	Store        string // scenario store backend: memory | file | postgres
}

func runNew(args []string) error {
	fs := flag.NewFlagSet("kiff new", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "USAGE:")
		fmt.Fprintln(os.Stderr, "  kiff new <module-path> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "FLAGS:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "EXAMPLES:")
		fmt.Fprintln(os.Stderr, "  kiff new github.com/acme/orders")
		fmt.Fprintln(os.Stderr, "  kiff new -template=agentic-ops github.com/acme/ops")
		fmt.Fprintln(os.Stderr, "  kiff new -scenario=refund github.com/acme/refunds")
	}
	dir := fs.String("dir", "", "directory to scaffold into (default: last segment of module path)")
	force := fs.Bool("force", false, "scaffold into a non-empty directory")
	replaceLocal := fs.String("replace-local", "", "emit a `replace github.com/kiff/kiff => <path>` directive in go.mod (use while the framework is unpublished)")
	templateName := fs.String("template", templateStarter, "scaffold template: starter (default) | agentic-ops")
	scenario := fs.String("scenario", "", "governed-action scenario: refund (generates a complete agent-on-a-risky-action project)")
	agentMode := fs.String("agent", "", "agent integration for a scenario: custom-http (default when -scenario is set)")
	storeMode := fs.String("store", "", "scenario store backend: file (default) | memory | postgres")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one argument: the module path")
	}

	// A scenario is a governed-action project template. When set, it selects
	// its own template and validates the chosen agent integration.
	store := "file"
	isScenario := strings.TrimSpace(*scenario) != ""
	if isScenario {
		resolved, err := resolveScenarioTemplate(*scenario, *agentMode)
		if err != nil {
			fs.Usage()
			return err
		}
		*templateName = resolved
		s, err := resolveStore(*storeMode)
		if err != nil {
			fs.Usage()
			return err
		}
		store = s
	} else {
		if strings.TrimSpace(*agentMode) != "" {
			return errors.New("-agent requires -scenario")
		}
		if strings.TrimSpace(*storeMode) != "" {
			return errors.New("-store requires -scenario")
		}
	}

	tmpl, err := resolveTemplate(*templateName)
	if err != nil {
		fs.Usage()
		return err
	}

	modulePath := strings.TrimSpace(fs.Arg(0))
	if err := validateModulePath(modulePath); err != nil {
		return err
	}
	moduleName := path.Base(modulePath)

	target := *dir
	if target == "" {
		target = moduleName
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	if err := ensureTargetDir(target, *force); err != nil {
		return err
	}

	data := templateData{
		ModulePath:   modulePath,
		ModuleName:   moduleName,
		GoVersion:    StarterGoVersion,
		KiffVersion:  StarterKiffVersion,
		ReplaceLocal: strings.TrimSpace(*replaceLocal),
		Store:        store,
	}

	if err := scaffold(target, tmpl, data); err != nil {
		return err
	}

	fmt.Println("created KIFF project")
	fmt.Printf("  module   : %s\n", modulePath)
	fmt.Printf("  template : %s\n", tmpl.Name)
	fmt.Printf("  path     : %s\n", target)
	fmt.Println("")
	fmt.Println("next steps:")
	rel, _ := filepath.Rel(mustGetwd(), target)
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = target
	}
	fmt.Printf("  cd %s\n", rel)
	fmt.Println("  go mod tidy")
	switch tmpl.Name {
	case templateAgenticOps, templateScenarioRefund:
		fmt.Println("  make demo")
	default:
		fmt.Println("  go run ./cmd/server")
	}
	return nil
}

// templateSpec captures the per-template knobs: the embedded fs, root
// path within that fs, and the import prefix to rewrite into the user's
// module path.
type templateSpec struct {
	Name         string
	FS           embed.FS
	Root         string
	ImportPrefix string
}

// resolveStore validates the -store choice for a scenario. Default is file
// (persistent, zero-Docker). Postgres pulls in local-dev wiring; memory is the
// non-persistent opt-out.
func resolveStore(store string) (string, error) {
	switch store {
	case "":
		return "file", nil
	case "file", "memory", "postgres":
		return store, nil
	default:
		return "", fmt.Errorf("unknown -store %q (known: file, memory, postgres)", store)
	}
}

// scenarioPostgresOnly reports whether a scaffolded file should be emitted only
// when the postgres store is selected. Keeps file/memory scaffolds free of
// Postgres wiring (and its pgx dependency).
func scenarioPostgresOnly(rel string) bool {
	switch rel {
	case "docker-compose.yml", ".env.example", filepath.Join("cmd", "server", "store_postgres.go"):
		return true
	}
	return false
}

func resolveTemplate(name string) (templateSpec, error) {
	switch name {
	case templateStarter, "":
		return templateSpec{Name: templateStarter, FS: starterFS, Root: starterRoot, ImportPrefix: starterImportPrefix}, nil
	case templateAgenticOps:
		return templateSpec{Name: templateAgenticOps, FS: agenticOpsFS, Root: agenticOpsRoot, ImportPrefix: agenticOpsImport}, nil
	case templateScenarioRefund:
		return templateSpec{Name: templateScenarioRefund, FS: scenarioRefundFS, Root: scenarioRefundRoot, ImportPrefix: scenarioRefundImport}, nil
	default:
		return templateSpec{}, fmt.Errorf("unknown template: %q (known: starter, agentic-ops, scenario-refund)", name)
	}
}

// resolveScenarioTemplate validates a -scenario/-agent pair and returns the
// template name that implements it. The generated project is identical across
// agents except the agent-facing wrapper; today only custom-http ships in the
// framework repo (deeper adapters live in kiff-guard).
func resolveScenarioTemplate(scenario, agentMode string) (string, error) {
	switch scenario {
	case "refund":
		switch agentMode {
		case "", "custom-http":
			return templateScenarioRefund, nil
		default:
			return "", fmt.Errorf("unknown -agent %q for scenario refund (known: custom-http)", agentMode)
		}
	default:
		return "", fmt.Errorf("unknown -scenario %q (known: refund)", scenario)
	}
}

// scaffold walks the embedded template, transforms each file, and writes
// it to target. Files ending in .tmpl are rendered as text/template;
// the trailing .tmpl is stripped from the output. All other files have
// their template-specific import prefix rewritten to the user's module
// path.
func scaffold(target string, tmpl templateSpec, data templateData) error {
	return fs.WalkDir(tmpl.FS, tmpl.Root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(tmpl.Root, p)
		if err != nil {
			return err
		}
		// Postgres wiring is emitted only for the postgres store, so file and
		// memory scaffolds stay free of the pgx dependency.
		if data.Store != "postgres" && scenarioPostgresOnly(rel) {
			return nil
		}
		// Normalize *.tmpl to its non-tmpl name in the output. This covers
		// go.mod.tmpl and any future .tmpl files (Makefile.tmpl,
		// .gitignore.tmpl, etc).
		outRel := rel
		if strings.HasSuffix(outRel, ".tmpl") {
			outRel = strings.TrimSuffix(outRel, ".tmpl")
		}
		outPath := filepath.Join(target, outRel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		raw, err := tmpl.FS.ReadFile(p)
		if err != nil {
			return err
		}

		rendered, err := renderFile(rel, raw, tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", rel, err)
		}

		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") || strings.HasSuffix(outRel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(outPath, rendered, mode); err != nil {
			return err
		}
		return nil
	})
}

// renderFile applies the right transform for a given file:
//   - any .tmpl file runs through text/template
//   - README.md runs through text/template
//   - everything else has its template's in-tree import prefix rewritten
//     to the user's module path
func renderFile(rel string, raw []byte, tmpl templateSpec, data templateData) ([]byte, error) {
	if strings.HasSuffix(rel, ".tmpl") || filepath.Base(rel) == "README.md" {
		t, err := template.New(rel).Parse(string(raw))
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return rewriteImports(raw, tmpl.ImportPrefix, data.ModulePath), nil
}

// rewriteImports swaps the template's in-tree import prefix for the
// user's module path. We rewrite verbatim bytes rather than parsing AST
// because we want to preserve formatting exactly and the prefix is
// unambiguous within KIFF templates.
func rewriteImports(raw []byte, fromPrefix, modulePath string) []byte {
	if fromPrefix == "" {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte(fromPrefix), []byte(modulePath))
}

func validateModulePath(p string) error {
	if p == "" {
		return errors.New("module path is required")
	}
	if strings.ContainsAny(p, " \t\n") {
		return fmt.Errorf("module path must not contain whitespace: %q", p)
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("module path must not start or end with '/': %q", p)
	}
	if path.Base(p) == "" || path.Base(p) == "." {
		return fmt.Errorf("module path is missing a final segment: %q", p)
	}
	return nil
}

func ensureTargetDir(target string, force bool) error {
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(target, 0o755)
	}
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target exists and is not a directory: %s", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("read target: %w", err)
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("target directory %s is not empty (use -force to scaffold anyway)", target)
	}
	return nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
