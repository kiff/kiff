package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// errVerifyFailed is returned by runVerify when the domain has any error-level
// finding. main treats it as a quiet non-zero exit (the report is already
// printed), without prefixing a Go error message.
var errVerifyFailed = errors.New("verification failed")

type severity string

const (
	sevError   severity = "error"
	sevWarning severity = "warning"
)

type finding struct {
	Severity severity `json:"severity"`
	Code     string   `json:"code"`
	Action   string   `json:"action,omitempty"`
	State    string   `json:"state,omitempty"`
	Message  string   `json:"message"`
}

type verifyReport struct {
	Package  string    `json:"package"`
	Domain   string    `json:"domain,omitempty"`
	OK       bool      `json:"ok"`
	Findings []finding `json:"findings"`
}

func (r verifyReport) hasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == sevError {
			return true
		}
	}
	return false
}

// verifyDir analyzes the domain package at dir and returns its report.
func verifyDir(dir string) (verifyReport, error) {
	facts, err := parseDomainPackage(dir)
	if err != nil {
		return verifyReport{}, err
	}
	report := verifyFacts(facts)
	report.Package = dir
	return report, nil
}

// verifyFacts applies the structural checks to a domain's facts.
func verifyFacts(facts domainFacts) verifyReport {
	report := verifyReport{Domain: facts.Domain}
	var findings []finding
	add := func(f finding) { findings = append(findings, f) }

	// Index contracts by action name.
	contracts := map[string]factAction{}
	for _, a := range facts.Actions {
		contracts[a.Name] = a
	}

	// Distinguish "this isn't a KIFF domain" from "this domain is broken": a
	// path with no domain marker at all gets one clear finding, not a pile.
	if facts.Domain == "" && len(facts.Actions) == 0 && len(facts.Transitions) == 0 {
		report.Findings = []finding{{
			Severity: sevError,
			Code:     "not_a_domain",
			Message:  "no KIFF domain found here (no domain.New(...) or action contracts); check the path",
		}}
		report.OK = false
		return report
	}

	// Reachability: initial states are the targets of bootstrap transitions
	// (empty From). Walk transitions forward from there.
	reachable := map[string]bool{}
	var queue []string
	hasBootstrap := false
	for _, t := range facts.Transitions {
		if t.From == "" && t.To != "" {
			hasBootstrap = true
			if !reachable[t.To] {
				reachable[t.To] = true
				queue = append(queue, t.To)
			}
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, t := range facts.Transitions {
			if t.From == cur && t.To != "" && !reachable[t.To] {
				reachable[t.To] = true
				queue = append(queue, t.To)
			}
		}
	}

	if len(facts.Transitions) == 0 {
		add(finding{Severity: sevError, Code: "no_transitions", Message: "domain declares no state transitions"})
	} else if !hasBootstrap {
		add(finding{Severity: sevError, Code: "no_bootstrap", Message: "no bootstrap transition (a transition with an empty From) — no initial state is reachable"})
	}

	// 1. Executor backing + contract completeness, per action (stable order).
	actionNames := make([]string, 0, len(contracts))
	for name := range contracts {
		actionNames = append(actionNames, name)
	}
	sort.Strings(actionNames)
	for _, name := range actionNames {
		a := contracts[name]
		if !a.HasExecutor {
			add(finding{Severity: sevError, Code: "executor_missing", Action: name, Message: fmt.Sprintf("action %q has no executor", name)})
		} else if a.Stub {
			add(finding{Severity: sevError, Code: "executor_stub", Action: name, Message: fmt.Sprintf("action %q executor is a scaffold stub (contains TODO); implement it before this domain governs", name)})
		}
		if a.Risk == "" {
			add(finding{Severity: sevError, Code: "missing_risk", Action: name, Message: fmt.Sprintf("action %q does not declare a Risk", name)})
		} else if !validRisk(a.Risk) {
			add(finding{Severity: sevError, Code: "invalid_risk", Action: name, Message: fmt.Sprintf("action %q has invalid Risk %q (want low|medium|high|critical)", name, a.Risk)})
		}
		if a.Approval == "" {
			add(finding{Severity: sevError, Code: "missing_approval", Action: name, Message: fmt.Sprintf("action %q does not declare an ApprovalRequirement (use ApprovalNever or ApprovalRequired)", name)})
		} else if !validApproval(a.Approval) {
			add(finding{Severity: sevError, Code: "invalid_approval", Action: name, Message: fmt.Sprintf("action %q has invalid ApprovalRequirement %q (want never|required)", name, a.Approval)})
		}
		if len(a.AllowedStates) == 0 {
			add(finding{Severity: sevError, Code: "no_allowed_states", Action: name, Message: fmt.Sprintf("action %q declares no AllowedStates", name)})
		}
		// Each allowed state must be reachable.
		for _, s := range a.AllowedStates {
			if hasBootstrap && !reachable[s] {
				add(finding{Severity: sevError, Code: "unreachable_state", Action: name, State: s, Message: fmt.Sprintf("action %q is allowed from state %q, but no transition reaches it", name, s)})
			}
		}
	}

	// 2. Allowed actions must resolve to a contract.
	allowedStates := make([]string, 0, len(facts.AllowedStates))
	for s := range facts.AllowedStates {
		allowedStates = append(allowedStates, s)
	}
	sort.Strings(allowedStates)
	allowedActions := map[string]bool{}
	for _, s := range allowedStates {
		acts := append([]string(nil), facts.AllowedStates[s]...)
		sort.Strings(acts)
		for _, act := range acts {
			allowedActions[act] = true
			if _, ok := contracts[act]; !ok {
				add(finding{Severity: sevError, Code: "no_contract", Action: act, State: s, Message: fmt.Sprintf("action %q is allowed in state %q but has no contract", act, s)})
			}
		}
	}

	// 3. Transitions must reference declared events; warn on dead contracts.
	if len(facts.DeclaredEvents) > 0 {
		seen := map[string]bool{}
		for _, t := range facts.Transitions {
			if t.Event == "" || seen[t.Event] {
				continue
			}
			seen[t.Event] = true
			if !facts.DeclaredEvents[t.Event] {
				add(finding{Severity: sevError, Code: "undeclared_event", Message: fmt.Sprintf("transition references event %q, which the domain does not declare", t.Event)})
			}
		}
	}
	for _, name := range actionNames {
		if !allowedActions[name] && len(facts.AllowedStates) > 0 {
			add(finding{Severity: sevWarning, Code: "dead_action", Action: name, Message: fmt.Sprintf("action %q has a contract but is not allowed in any state", name)})
		}
	}

	if len(facts.Actions) == 0 {
		add(finding{Severity: sevError, Code: "no_actions", Message: "domain declares no action contracts"})
	}

	if findings == nil {
		findings = []finding{}
	}
	report.Findings = findings
	report.OK = !report.hasErrors()
	return report
}

func validRisk(r string) bool {
	switch r {
	case "low", "medium", "high", "critical":
		return true
	}
	return false
}

func validApproval(a string) bool {
	return a == "never" || a == "required"
}

// runVerify is the `kiff verify` entry point.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("kiff verify", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "USAGE:")
		fmt.Fprintln(os.Stderr, "  kiff verify [flags] [path]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Structurally verify a KIFF domain package: every action bound to a real")
		fmt.Fprintln(os.Stderr, "executor (no leftover scaffold stubs), a consistent state machine, and")
		fmt.Fprintln(os.Stderr, "complete action contracts. Exits non-zero on any error finding.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "path defaults to '.'; if <path>/domain is a package, it is used.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "FLAGS:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "EXAMPLES:")
		fmt.Fprintln(os.Stderr, "  kiff verify")
		fmt.Fprintln(os.Stderr, "  kiff verify ./domain")
		fmt.Fprintln(os.Stderr, "  kiff verify -json ./orders")
	}
	asJSON := fs.Bool("json", false, "emit the report as JSON for tooling/CI")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("expected at most one path argument")
	}

	path := "."
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	target, err := resolveDomainDir(path)
	if err != nil {
		return err
	}

	report, err := verifyDir(target)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printReport(os.Stdout, report)
	}

	if !report.OK {
		return errVerifyFailed
	}
	return nil
}

// resolveDomainDir picks the directory to analyze. If path itself is not the
// domain package, it probes common layouts (<path>/domain, <path>/internal/
// domain) and prefers the one that actually declares action contracts, so
// `kiff verify <project-root>` works for both the starter and agentic-ops
// layouts. Pointing directly at a package always works.
func resolveDomainDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}

	candidates := []string{
		path,
		filepath.Join(path, "domain"),
		filepath.Join(path, "internal", "domain"),
	}
	firstWithGo := ""
	for _, c := range candidates {
		if !hasGoFiles(c) {
			continue
		}
		if firstWithGo == "" {
			firstWithGo = c
		}
		if facts, err := parseDomainPackage(c); err == nil && len(facts.Actions) > 0 {
			return c, nil
		}
	}
	if firstWithGo != "" {
		return firstWithGo, nil
	}
	return path, nil
}

func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}

func printReport(w *os.File, r verifyReport) {
	var errCount, warnCount int
	for _, f := range r.Findings {
		switch f.Severity {
		case sevError:
			errCount++
		case sevWarning:
			warnCount++
		}
	}
	label := r.Package
	if r.Domain != "" {
		label = fmt.Sprintf("%s (domain %q)", r.Package, r.Domain)
	}
	if r.OK && warnCount == 0 {
		fmt.Fprintf(w, "OK  %s — domain is complete and consistent\n", label)
		return
	}
	fmt.Fprintf(w, "%s\n", label)
	for _, f := range r.Findings {
		loc := ""
		if f.Action != "" {
			loc = " [" + f.Action + "]"
		}
		fmt.Fprintf(w, "  %-7s %s%s: %s\n", strings.ToUpper(string(f.Severity)), f.Code, loc, f.Message)
	}
	fmt.Fprintf(w, "\n%d error(s), %d warning(s)\n", errCount, warnCount)
}
