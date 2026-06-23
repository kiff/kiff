package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// domainFacts is the structural picture of a domain package, extracted by
// static analysis of its source. It targets the conventional shape that
// `kiff new` and `kiff scaffold` emit: identifier constants, an action catalog
// of `action.ActionContract` literals, and a state graph declared via either
// `state.Transition{...}` literals + `SetAllowedActions`, or the
// `domain.Builder` chain (`.Event`, `.Transition`, `.Allow`).
type domainFacts struct {
	Domain         string
	DeclaredEvents map[string]bool     // events declared via .Event(...)
	Transitions    []factTransition    // union of literal + builder transitions
	AllowedStates  map[string][]string // state value -> action names allowed there
	Actions        []factAction        // one per ActionContract literal
}

type factTransition struct {
	Event string
	From  string
	To    string
}

type factAction struct {
	Name                string
	AllowedStates       []string
	Risk                string
	Approval            string
	RequiredParameters  []string
	RequiredPermissions []string
	HasExecutor         bool
	Stub                bool
}

// parseDomainPackage statically analyzes the Go package in dir and returns its
// domain facts. It does not build or run the package, so it is safe and fast,
// and works on a package whose imports are not resolvable.
func parseDomainPackage(dir string) (domainFacts, error) {
	facts := domainFacts{
		DeclaredEvents: map[string]bool{},
		AllowedStates:  map[string][]string{},
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return facts, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)

	fset := token.NewFileSet()
	type parsedFile struct {
		path string
		src  []byte
		file *ast.File
	}
	var parsed []parsedFile
	consts := map[string]string{}

	// Pass 1: parse every file and collect string-valued constants so values
	// resolve regardless of declaration order or file split.
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			return facts, err
		}
		f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			return facts, err
		}
		parsed = append(parsed, parsedFile{path: p, src: src, file: f})
		collectConsts(f, consts)
	}

	// Pass 2: extract the domain facts.
	for _, pf := range parsed {
		ex := &extractor{
			fset:   fset,
			src:    pf.src,
			base:   fset.File(pf.file.Pos()).Base(),
			consts: consts,
			facts:  &facts,
		}
		ast.Inspect(pf.file, ex.visit)
	}

	return facts, nil
}

func collectConsts(f *ast.File, out map[string]string) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil {
						out[name.Name] = v
					}
				}
			}
		}
	}
}

type extractor struct {
	fset   *token.FileSet
	src    []byte
	base   int
	consts map[string]string
	facts  *domainFacts
}

func (e *extractor) visit(n ast.Node) bool {
	switch node := n.(type) {
	case *ast.CompositeLit:
		switch t := node.Type.(type) {
		case *ast.SelectorExpr:
			switch t.Sel.Name {
			case "ActionContract":
				e.addAction(node)
			case "Transition":
				e.addTransition(e.transitionFromLiteral(node))
			}
		case *ast.ArrayType:
			// Inline catalogs like []action.ActionContract{ {Name: ...}, ... }
			// declare each element with an elided type (CompositeLit.Type == nil),
			// so the elements are not caught by the SelectorExpr case above.
			if sel, ok := t.Elt.(*ast.SelectorExpr); ok && sel.Sel.Name == "ActionContract" {
				for _, el := range node.Elts {
					if cl, ok := el.(*ast.CompositeLit); ok {
						e.addAction(cl)
					}
				}
			}
		}
	case *ast.CallExpr:
		if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
			xName := ""
			if id, ok := sel.X.(*ast.Ident); ok {
				xName = id.Name
			}
			e.handleCall(xName, sel.Sel.Name, node.Args)
		}
	}
	return true
}

// addAction records a contract from an ActionContract literal, skipping
// zero-value literals (no Name) such as the `return action.ActionContract{}`
// found in lookup helpers, and de-duplicating by name.
func (e *extractor) addAction(lit *ast.CompositeLit) {
	a := e.actionFromLiteral(lit)
	if a.Name == "" {
		return
	}
	for _, existing := range e.facts.Actions {
		if existing.Name == a.Name {
			return
		}
	}
	e.facts.Actions = append(e.facts.Actions, a)
}

func (e *extractor) handleCall(xName, name string, args []ast.Expr) {
	switch name {
	case "New":
		// Only the domain builder, e.g. domain.New("x") / kiffdomain.New("x").
		// Excludes errors.New and other unrelated New calls.
		if (xName == "domain" || xName == "kiffdomain") && len(args) == 1 {
			if v, ok := e.str(args[0]); ok && e.facts.Domain == "" {
				e.facts.Domain = v
			}
		}
	case "Event":
		if len(args) == 1 {
			if v, ok := e.str(args[0]); ok {
				e.facts.DeclaredEvents[v] = true
			}
		}
	case "Transition":
		if len(args) == 3 {
			e.addTransition(factTransition{
				Event: e.strOr(args[0]),
				From:  e.strOr(args[1]),
				To:    e.strOr(args[2]),
			})
		}
	case "Allow":
		if len(args) >= 2 {
			state := e.strOr(args[0])
			for _, a := range args[1:] {
				e.addAllowed(state, e.strOr(a))
			}
		}
	case "SetAllowedActions":
		if len(args) == 2 {
			state := e.strOr(args[0])
			for _, a := range e.strList(args[1]) {
				e.addAllowed(state, a)
			}
		}
	}
}

func (e *extractor) addTransition(t factTransition) {
	for _, existing := range e.facts.Transitions {
		if existing == t {
			return
		}
	}
	e.facts.Transitions = append(e.facts.Transitions, t)
}

func (e *extractor) addAllowed(state, action string) {
	if state == "" || action == "" {
		return
	}
	for _, a := range e.facts.AllowedStates[state] {
		if a == action {
			return
		}
	}
	e.facts.AllowedStates[state] = append(e.facts.AllowedStates[state], action)
}

func (e *extractor) transitionFromLiteral(lit *ast.CompositeLit) factTransition {
	var t factTransition
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "EventType":
			t.Event = e.strOr(kv.Value)
		case "From":
			t.From = e.strOr(kv.Value)
		case "To":
			t.To = e.strOr(kv.Value)
		}
	}
	return t
}

func (e *extractor) actionFromLiteral(lit *ast.CompositeLit) factAction {
	a := factAction{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "Name":
			a.Name = e.strOr(kv.Value)
		case "AllowedStates":
			a.AllowedStates = e.strList(kv.Value)
		case "RequiredParameters":
			a.RequiredParameters = e.strList(kv.Value)
		case "RequiredPermissions":
			a.RequiredPermissions = e.strList(kv.Value)
		case "Risk":
			a.Risk = e.enumSuffix(kv.Value, "Risk")
		case "ApprovalRequirement":
			a.Approval = e.enumSuffix(kv.Value, "Approval")
		case "Executor":
			a.HasExecutor = true
			a.Stub = e.bodyHasTODO(kv.Value)
		}
	}
	return a
}

// str resolves an expression to a string value when it is a string literal or
// a known string constant. The bool reports whether resolution succeeded.
func (e *extractor) str(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.Ident:
		if s, ok := e.consts[v.Name]; ok {
			return s, true
		}
	case *ast.SelectorExpr:
		if s, ok := e.consts[v.Sel.Name]; ok {
			return s, true
		}
	}
	return "", false
}

// strOr resolves to a string value, falling back to the identifier name when
// the constant is unknown (so cross-references still compare consistently).
func (e *extractor) strOr(expr ast.Expr) string {
	if s, ok := e.str(expr); ok {
		return s
	}
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

func (e *extractor) strList(expr ast.Expr) []string {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var out []string
	for _, el := range lit.Elts {
		if s := e.strOr(el); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// enumSuffix turns a selector like action.RiskLow / action.ApprovalRequired
// into its lowercase suffix ("low", "required"), matching the framework's
// RiskLevel / ApprovalRequirement string values.
func (e *extractor) enumSuffix(expr ast.Expr, prefix string) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	name := sel.Sel.Name
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(name, prefix))
}

// bodyHasTODO reports whether a FuncLit's body source contains a TODO marker —
// the signal a scaffolded executor stub leaves behind.
func (e *extractor) bodyHasTODO(expr ast.Expr) bool {
	fl, ok := expr.(*ast.FuncLit)
	if !ok || fl.Body == nil {
		return false
	}
	start := e.offset(fl.Body.Pos())
	end := e.offset(fl.Body.End())
	if start < 0 || end > len(e.src) || start >= end {
		return false
	}
	return strings.Contains(string(e.src[start:end]), "TODO")
}

func (e *extractor) offset(pos token.Pos) int {
	return e.fset.Position(pos).Offset
}
