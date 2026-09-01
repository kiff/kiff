package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var errScanFindings = errors.New("findings at or above failure threshold")

type scanSeverity string

const (
	scanLow    scanSeverity = "low"
	scanMedium scanSeverity = "medium"
	scanHigh   scanSeverity = "high"
)

type goScanFinding struct {
	RuleID      string       `json:"rule_id"`
	Severity    scanSeverity `json:"severity"`
	Category    string       `json:"category"`
	Tool        string       `json:"tool"`
	Call        string       `json:"call"`
	File        string       `json:"file"`
	Line        int          `json:"line"`
	Explanation string       `json:"explanation"`
}

type goScanSummary struct {
	Files          int `json:"files"`
	Tools          int `json:"tools"`
	ReviewRequired int `json:"review_required"`
	High           int `json:"high"`
	Medium         int `json:"medium"`
	Low            int `json:"low"`
}

type goScanReport struct {
	SchemaVersion int             `json:"schema_version"`
	Language      string          `json:"language"`
	Summary       goScanSummary   `json:"summary"`
	Findings      []goScanFinding `json:"findings"`
}

type stringFlags []string

func (s *stringFlags) String() string { return strings.Join(*s, ",") }

func (s *stringFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

type scanOptions struct {
	Tools  []string
	Guards []string
}

func runScan(args []string) error {
	return runScanWithIO(os.Stdout, os.Stderr, args)
}

func runScanWithIO(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("kiff scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "USAGE:")
		fmt.Fprintln(stderr, "  kiff scan [flags] [path]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Statically inspect Go agent tools for consequential calls with no")
		fmt.Fprintln(stderr, "recognized decision or guard earlier in the function.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "A tool is explicit: mark it with //kiff:tool, pass it to a common")
		fmt.Fprintln(stderr, "tool-registration call, or name it with -tool.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "FLAGS:")
		fs.PrintDefaults()
	}
	format := fs.String("format", "text", "output format: text, json, or sarif")
	output := fs.String("output", "", "write output to a file instead of stdout")
	failOn := fs.String("fail-on", "medium", "fail at this severity: none, low, medium, or high")
	var tools stringFlags
	var guards stringFlags
	fs.Var(&tools, "tool", "additional Go function to treat as an agent tool (repeatable)")
	fs.Var(&guards, "guard", "additional guard function name (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("expected at most one path argument")
	}
	if !validScanFormat(*format) {
		return fmt.Errorf("unknown format %q", *format)
	}
	if _, ok := scanSeverityRank(*failOn); !ok {
		return fmt.Errorf("unknown fail-on severity %q", *failOn)
	}

	path := "."
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	report, err := scanGoPath(path, scanOptions{Tools: tools, Guards: guards})
	if err != nil {
		return err
	}

	var rendered []byte
	switch *format {
	case "json":
		rendered, err = json.MarshalIndent(report, "", "  ")
	case "sarif":
		rendered, err = json.MarshalIndent(scanSARIF(report), "", "  ")
	default:
		rendered = []byte(renderScanText(report))
	}
	if err != nil {
		return err
	}
	rendered = append(rendered, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, rendered, 0o644); err != nil {
			return err
		}
	} else if _, err := stdout.Write(rendered); err != nil {
		return err
	}

	if reportMeetsThreshold(report, *failOn) {
		return errScanFindings
	}
	return nil
}

func validScanFormat(format string) bool {
	return format == "text" || format == "json" || format == "sarif"
}

func scanSeverityRank(value string) (int, bool) {
	switch value {
	case "none":
		return 99, true
	case "low":
		return 1, true
	case "medium":
		return 2, true
	case "high":
		return 3, true
	default:
		return 0, false
	}
}

func reportMeetsThreshold(report goScanReport, failOn string) bool {
	threshold, _ := scanSeverityRank(failOn)
	for _, finding := range report.Findings {
		rank, _ := scanSeverityRank(string(finding.Severity))
		if rank >= threshold {
			return true
		}
	}
	return false
}

type parsedGoFile struct {
	path string
	file *ast.File
}

func scanGoPath(root string, options scanOptions) (goScanReport, error) {
	info, err := os.Stat(root)
	if err != nil {
		return goScanReport{}, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return goScanReport{}, err
	}

	var paths []string
	if !info.IsDir() {
		if filepath.Ext(root) != ".go" {
			return goScanReport{}, fmt.Errorf("not a Go file: %s", root)
		}
		paths = append(paths, rootAbs)
		rootAbs = filepath.Dir(rootAbs)
	} else {
		err = filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && path != rootAbs && ignoredScanDir(entry.Name()) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return goScanReport{}, err
		}
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	files := make([]parsedGoFile, 0, len(paths))
	functions := map[string]*ast.FuncDecl{}
	for _, path := range paths {
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return goScanReport{}, parseErr
		}
		files = append(files, parsedGoFile{path: path, file: file})
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				functions[fn.Name.Name] = fn
			}
		}
	}

	toolNames := map[string]bool{}
	for _, name := range options.Tools {
		toolNames[name] = true
	}
	for _, parsed := range files {
		collectExplicitTools(parsed.file, toolNames)
	}

	guards := defaultGoGuards()
	for _, name := range options.Guards {
		guards[normalizeCallName(name)] = true
	}
	report := goScanReport{
		SchemaVersion: 1,
		Language:      "go",
		Findings:      []goScanFinding{},
		Summary: goScanSummary{
			Files: len(files),
		},
	}
	for name := range toolNames {
		fn := functions[name]
		if fn == nil {
			continue
		}
		report.Summary.Tools++
		for _, finding := range inspectGoTool(rootAbs, fset, name, fn, guards) {
			report.Findings = append(report.Findings, finding)
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].File == report.Findings[j].File {
			return report.Findings[i].Line < report.Findings[j].Line
		}
		return report.Findings[i].File < report.Findings[j].File
	})
	report.Summary.ReviewRequired = len(report.Findings)
	for _, finding := range report.Findings {
		switch finding.Severity {
		case scanHigh:
			report.Summary.High++
		case scanMedium:
			report.Summary.Medium++
		case scanLow:
			report.Summary.Low++
		}
	}
	return report, nil
}

func ignoredScanDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "vendor", "node_modules", "testdata", "dist", "build":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

var toolRegistrationCalls = map[string]bool{
	"addtool": true, "definetool": true, "infertool": true, "newtool": true,
	"registertool": true, "tool": true, "withtool": true, "withtools": true,
}

func collectExplicitTools(file *ast.File, tools map[string]bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && hasToolDirective(fn.Doc) {
			tools[fn.Name.Name] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			if !toolRegistrationCalls[normalizeCallName(callName(value.Fun))] {
				return true
			}
			for _, arg := range value.Args {
				collectFunctionReference(arg, tools)
			}
		case *ast.CompositeLit:
			for _, element := range value.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch strings.ToLower(key.Name) {
				case "func", "function", "handler", "execute":
					collectFunctionReference(kv.Value, tools)
				}
			}
		}
		return true
	})
}

func hasToolDirective(comments *ast.CommentGroup) bool {
	if comments == nil {
		return false
	}
	for _, comment := range comments.List {
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		if text == "kiff:tool" {
			return true
		}
	}
	return false
}

func collectFunctionReference(expr ast.Expr, tools map[string]bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name != "nil" && value.Name != "true" && value.Name != "false" {
			tools[value.Name] = true
		}
	case *ast.UnaryExpr:
		collectFunctionReference(value.X, tools)
	}
}

type sinkDefinition struct {
	Category string
	Severity scanSeverity
}

var goSinks = map[string]sinkDefinition{
	"charge":            {Category: "money movement", Severity: scanHigh},
	"createpayment":     {Category: "money movement", Severity: scanHigh},
	"deletebucket":      {Category: "data loss", Severity: scanHigh},
	"deletedatabase":    {Category: "data loss", Severity: scanHigh},
	"deleteobject":      {Category: "data loss", Severity: scanHigh},
	"disableuser":       {Category: "access control", Severity: scanHigh},
	"dropdatabase":      {Category: "data loss", Severity: scanHigh},
	"exec":              {Category: "code execution", Severity: scanHigh},
	"issuepayout":       {Category: "money movement", Severity: scanHigh},
	"publish":           {Category: "external communication", Severity: scanMedium},
	"refund":            {Category: "money movement", Severity: scanHigh},
	"revokeaccess":      {Category: "access control", Severity: scanHigh},
	"runcommand":        {Category: "code execution", Severity: scanHigh},
	"sendemail":         {Category: "external communication", Severity: scanMedium},
	"terminateinstance": {Category: "infrastructure", Severity: scanHigh},
	"transfer":          {Category: "money movement", Severity: scanHigh},
	"updatebankdetails": {Category: "money movement", Severity: scanHigh},
}

func defaultGoGuards() map[string]bool {
	return map[string]bool{
		"authorize": true, "checkpermission": true, "decide": true,
		"enforce": true, "evaluate": true, "requireapproval": true,
		"validateaction": true, "validateproposal": true,
	}
}

func inspectGoTool(root string, fset *token.FileSet, tool string, fn *ast.FuncDecl, guards map[string]bool) []goScanFinding {
	var guardedAt []token.Pos
	type sinkCall struct {
		pos  token.Pos
		name string
		def  sinkDefinition
	}
	var sinks []sinkCall
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := normalizeCallName(callName(call.Fun))
		if guards[name] {
			guardedAt = append(guardedAt, call.Pos())
		}
		if def, ok := goSinks[name]; ok {
			sinks = append(sinks, sinkCall{pos: call.Pos(), name: callName(call.Fun), def: def})
		}
		return true
	})

	findings := []goScanFinding{}
	for _, sink := range sinks {
		guarded := false
		for _, pos := range guardedAt {
			if pos < sink.pos {
				guarded = true
				break
			}
		}
		if guarded {
			continue
		}
		position := fset.Position(sink.pos)
		rel, err := filepath.Rel(root, position.Filename)
		if err != nil {
			rel = position.Filename
		}
		findings = append(findings, goScanFinding{
			RuleID:      "KIFF-GO-001",
			Severity:    sink.def.Severity,
			Category:    sink.def.Category,
			Tool:        tool,
			Call:        sink.name,
			File:        filepath.ToSlash(rel),
			Line:        position.Line,
			Explanation: "agent tool reaches a consequential call with no recognized decision earlier in the function",
		})
	}
	return findings
}

func callName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := callName(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	case *ast.IndexExpr:
		return callName(value.X)
	case *ast.IndexListExpr:
		return callName(value.X)
	default:
		return ""
	}
}

func normalizeCallName(name string) string {
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	name = strings.ToLower(name)
	return strings.NewReplacer("_", "", "-", "").Replace(name)
}

func renderScanText(report goScanReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kiff scan: %d Go files, %d agent tools\n", report.Summary.Files, report.Summary.Tools)
	if len(report.Findings) == 0 {
		b.WriteString("no supported unguarded consequential paths found\n")
		return strings.TrimSuffix(b.String(), "\n")
	}
	fmt.Fprintf(&b, "%d path(s) require review\n\n", report.Summary.ReviewRequired)
	for _, finding := range report.Findings {
		fmt.Fprintf(&b, "%s %s:%d  %s -> %s  (%s)\n",
			strings.ToUpper(string(finding.Severity)), finding.File, finding.Line,
			finding.Tool, finding.Call, finding.Category)
	}
	b.WriteString("\nStatic analysis did not establish external reachability or exploitability.")
	return b.String()
}

func scanSARIF(report goScanReport) map[string]any {
	results := make([]map[string]any, 0, len(report.Findings))
	for _, finding := range report.Findings {
		level := "warning"
		if finding.Severity == scanHigh {
			level = "error"
		} else if finding.Severity == scanLow {
			level = "note"
		}
		results = append(results, map[string]any{
			"ruleId": finding.RuleID,
			"level":  level,
			"message": map[string]any{
				"text": finding.Explanation + ": " + finding.Tool + " -> " + finding.Call,
			},
			"locations": []map[string]any{{
				"physicalLocation": map[string]any{
					"artifactLocation": map[string]any{"uri": finding.File},
					"region":           map[string]any{"startLine": finding.Line},
				},
			}},
		})
	}
	return map[string]any{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs": []map[string]any{{
			"tool": map[string]any{"driver": map[string]any{
				"name":           "kiff scan",
				"informationUri": "https://github.com/kiff/kiff",
				"rules": []map[string]any{{
					"id": "KIFF-GO-001",
					"shortDescription": map[string]any{
						"text": "Unguarded consequential agent action",
					},
				}},
			}},
			"results": results,
		}},
	}
}
