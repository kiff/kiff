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

const (
	starterRoot         = "templates/starter"
	starterImportPrefix = "github.com/kiff-framework/kiff-framework/cmd/kiff/templates/starter"
	goModTemplateName   = "go.mod.tmpl"
)

// templateData feeds text/template rendering for files like go.mod.tmpl and
// README.md.
type templateData struct {
	ModulePath   string // full module path, e.g. github.com/acme/orders
	ModuleName   string // last path segment, e.g. orders
	GoVersion    string
	KiffVersion  string
	ReplaceLocal string // optional path used to emit a `replace` directive
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
		fmt.Fprintln(os.Stderr, "EXAMPLE:")
		fmt.Fprintln(os.Stderr, "  kiff new github.com/acme/orders")
	}
	dir := fs.String("dir", "", "directory to scaffold into (default: last segment of module path)")
	force := fs.Bool("force", false, "scaffold into a non-empty directory")
	replaceLocal := fs.String("replace-local", "", "emit a `replace github.com/kiff-framework/kiff-framework => <path>` directive in go.mod (use while the framework is unpublished)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one argument: the module path")
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
	target, err := filepath.Abs(target)
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
	}

	if err := scaffold(target, data); err != nil {
		return err
	}

	fmt.Println("created KIFF project")
	fmt.Printf("  module : %s\n", modulePath)
	fmt.Printf("  path   : %s\n", target)
	fmt.Println("")
	fmt.Println("next steps:")
	rel, _ := filepath.Rel(mustGetwd(), target)
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = target
	}
	fmt.Printf("  cd %s\n", rel)
	fmt.Println("  go mod tidy")
	fmt.Println("  go run ./cmd/server")
	return nil
}

// scaffold walks the embedded starter, transforms each file, and writes it to
// target. Files ending in .tmpl are rendered as text/template; all other files
// have their KIFF starter import paths rewritten to the user's module path.
func scaffold(target string, data templateData) error {
	return fs.WalkDir(starterFS, starterRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(starterRoot, p)
		if err != nil {
			return err
		}
		// Normalize go.mod.tmpl to go.mod in the output.
		outRel := rel
		if filepath.Base(outRel) == goModTemplateName {
			outRel = filepath.Join(filepath.Dir(outRel), "go.mod")
		}
		outPath := filepath.Join(target, outRel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		raw, err := starterFS.ReadFile(p)
		if err != nil {
			return err
		}

		rendered, err := renderFile(rel, raw, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", rel, err)
		}

		mode := os.FileMode(0o644)
		if err := os.WriteFile(outPath, rendered, mode); err != nil {
			return err
		}
		return nil
	})
}

// renderFile applies the right transform for a given file:
//   - go.mod.tmpl and README.md run through text/template
//   - all other files are byte-rewritten to swap the embedded starter's
//     import path for the user's module path
func renderFile(rel string, raw []byte, data templateData) ([]byte, error) {
	switch filepath.Base(rel) {
	case goModTemplateName, "README.md":
		tmpl, err := template.New(rel).Parse(string(raw))
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return rewriteImports(raw, data.ModulePath), nil
}

// rewriteImports swaps the starter's in-tree import prefix for the user's
// module path. We rewrite verbatim bytes rather than parsing AST because we
// want to preserve formatting exactly and the prefix is unambiguous.
func rewriteImports(raw []byte, modulePath string) []byte {
	return bytes.ReplaceAll(raw, []byte(starterImportPrefix), []byte(modulePath))
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
