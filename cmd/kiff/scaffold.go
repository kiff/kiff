package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

// scaffoldDescriptor is a one-time code-generation seed read by `kiff
// scaffold`. It is NOT a runtime artifact: the framework runtime still builds
// domains exclusively via domain.Builder. This type lives under cmd/kiff (not
// pkg/kiff) on purpose, so it can never be mistaken for a declarative runtime
// loader. See issue #27.
type scaffoldDescriptor struct {
	Domain      string                 `json:"domain"`
	Entity      string                 `json:"entity"`
	Adapter     string                 `json:"adapter"`
	Events      []string               `json:"events"`
	States      []string               `json:"states"`
	Transitions []descriptorTransition `json:"transitions"`
	Actions     []descriptorAction     `json:"actions"`
	Roles       map[string][]string    `json:"roles"`
}

type descriptorTransition struct {
	On   string `json:"on"`
	From string `json:"from"`
	To   string `json:"to"`
}

type descriptorAction struct {
	Name                string   `json:"name"`
	AllowedStates       []string `json:"allowed_states"`
	RequiredParameters  []string `json:"required_parameters"`
	RequiredPermissions []string `json:"required_permissions"`
	Risk                string   `json:"risk"`
	Approval            string   `json:"approval"`
	FollowUpEvents      []string `json:"follow_up_events"`
}

// parseDescriptor decodes and validates a descriptor from r.
func parseDescriptor(r io.Reader) (scaffoldDescriptor, error) {
	var d scaffoldDescriptor
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return scaffoldDescriptor{}, fmt.Errorf("decode descriptor: %w", err)
	}
	if err := d.validate(); err != nil {
		return scaffoldDescriptor{}, err
	}
	return d, nil
}

func (d scaffoldDescriptor) validate() error {
	if strings.TrimSpace(d.Domain) == "" {
		return errors.New("descriptor: domain is required")
	}
	if strings.TrimSpace(d.Entity) == "" {
		return errors.New("descriptor: entity is required")
	}
	if len(d.States) == 0 {
		return errors.New("descriptor: at least one state is required")
	}
	if len(d.Events) == 0 {
		return errors.New("descriptor: at least one event is required")
	}
	if len(d.Actions) == 0 {
		return errors.New("descriptor: at least one action is required")
	}

	stateSet := toSet(d.States)
	eventSet := toSet(d.Events)

	bootstrap := 0
	for _, tr := range d.Transitions {
		if tr.On == "" {
			return errors.New("descriptor: transition is missing 'on' event")
		}
		if !eventSet[tr.On] {
			return fmt.Errorf("descriptor: transition references unknown event %q", tr.On)
		}
		if tr.To == "" || !stateSet[tr.To] {
			return fmt.Errorf("descriptor: transition on %q references unknown 'to' state %q", tr.On, tr.To)
		}
		if tr.From == "" {
			bootstrap++
		} else if !stateSet[tr.From] {
			return fmt.Errorf("descriptor: transition on %q references unknown 'from' state %q", tr.On, tr.From)
		}
	}
	if bootstrap == 0 {
		return errors.New("descriptor: needs a bootstrap transition with an empty 'from' state")
	}

	for _, a := range d.Actions {
		if strings.TrimSpace(a.Name) == "" {
			return errors.New("descriptor: action name is required")
		}
		if len(a.AllowedStates) == 0 {
			return fmt.Errorf("descriptor: action %q needs at least one allowed_state", a.Name)
		}
		for _, s := range a.AllowedStates {
			if !stateSet[s] {
				return fmt.Errorf("descriptor: action %q allowed_state %q is not a declared state", a.Name, s)
			}
		}
		for _, e := range a.FollowUpEvents {
			if !eventSet[e] {
				return fmt.Errorf("descriptor: action %q follow_up_event %q is not a declared event", a.Name, e)
			}
		}
		if _, err := riskExpr(a.Risk); err != nil {
			return fmt.Errorf("descriptor: action %q: %w", a.Name, err)
		}
		if _, err := approvalExpr(a.Approval); err != nil {
			return fmt.Errorf("descriptor: action %q: %w", a.Name, err)
		}
	}
	return nil
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, i := range items {
		s[i] = true
	}
	return s
}

func riskExpr(risk string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "", "low":
		return "action.RiskLow", nil
	case "medium":
		return "action.RiskMedium", nil
	case "high":
		return "action.RiskHigh", nil
	case "critical":
		return "action.RiskCritical", nil
	default:
		return "", fmt.Errorf("unknown risk %q (want low|medium|high|critical)", risk)
	}
}

func approvalExpr(approval string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(approval)) {
	case "", "never":
		return "action.ApprovalNever", nil
	case "required":
		return "action.ApprovalRequired", nil
	default:
		return "", fmt.Errorf("unknown approval %q (want never|required)", approval)
	}
}

// --- identifier derivation -------------------------------------------------

func titleWord(w string) string {
	if w == "" {
		return ""
	}
	r := []rune(strings.ToLower(w))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// pascal converts an UPPER_SNAKE or dotted.lowercase token to PascalCase.
func pascal(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(titleWord(p))
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func eventConst(v string) string  { return "Event" + pascal(v) }
func stateConst(v string) string  { return "State" + pascal(v) }
func actionConst(v string) string { return "Action" + pascal(v) }
func permConst(v string) string   { return "Perm" + pascal(v) }

// --- generation model ------------------------------------------------------

type genConst struct {
	Name  string
	Value string
}

type genTransition struct {
	EventConst string
	FromExpr   string // a state const, or `""`
	ToExpr     string
}

type genAllowed struct {
	StateConst string
	ActionList string // comma-joined action consts
}

type genAction struct {
	Name                    string
	Const                   string
	Func                    string
	AllowedStatesList       string
	RequiredParametersList  string
	RequiredPermissionsList string
	Risk                    string
	Approval                string
	RequiresApproval        bool
	FollowUpEvents          []string // event consts
}

type genRole struct {
	Name       string
	PermConsts []string
}

type genStep struct {
	ActionConst      string
	ActionName       string
	FromState        string
	ToState          string
	RequiresApproval bool
	ParamsLiteral    string
	Index            int
}

type genActionTest struct {
	Pascal           string
	Const            string
	Func             string
	AllowedState     string
	WrongStateExpr   string
	RequiresApproval bool
	ParamsLiteral    string
}

type genModel struct {
	Domain       string
	Adapter      string
	AdapterConst string
	Entity       string
	EntityConst  string

	Events  []genConst
	States  []genConst
	Actions []genAction
	Perms   []genConst
	Roles   []genRole

	Transitions    []genTransition
	AllowedByState []genAllowed

	// test-only
	BootstrapEventConst string
	InitialState        string
	Steps               []genStep
	FinalState          string
	ActionTests         []genActionTest
	UsesApprovalImport  bool
}

func buildModel(d scaffoldDescriptor) (genModel, error) {
	m := genModel{
		Domain:       d.Domain,
		Adapter:      defaultString(d.Adapter, d.Domain),
		AdapterConst: "Adapter" + pascal(defaultString(d.Adapter, d.Domain)),
		Entity:       d.Entity,
		EntityConst:  "Entity" + pascal(d.Entity),
	}

	for _, e := range d.Events {
		m.Events = append(m.Events, genConst{Name: eventConst(e), Value: e})
	}
	for _, s := range d.States {
		m.States = append(m.States, genConst{Name: stateConst(s), Value: s})
	}

	// Collect permissions in stable order (first seen across roles + actions).
	permSeen := map[string]bool{}
	addPerm := func(p string) {
		if p == "" || permSeen[p] {
			return
		}
		permSeen[p] = true
		m.Perms = append(m.Perms, genConst{Name: permConst(p), Value: p})
	}
	// roles sorted for determinism
	roleNames := make([]string, 0, len(d.Roles))
	for r := range d.Roles {
		roleNames = append(roleNames, r)
	}
	sort.Strings(roleNames)
	for _, r := range roleNames {
		perms := d.Roles[r]
		consts := make([]string, 0, len(perms))
		for _, p := range perms {
			addPerm(p)
			consts = append(consts, permConst(p))
		}
		m.Roles = append(m.Roles, genRole{Name: r, PermConsts: consts})
	}
	for _, a := range d.Actions {
		for _, p := range a.RequiredPermissions {
			addPerm(p)
		}
	}

	// Detect const-name collisions that would break compilation.
	if err := assertUniqueConsts(m); err != nil {
		return genModel{}, err
	}

	// Transitions.
	for _, tr := range d.Transitions {
		from := `""`
		if tr.From != "" {
			from = stateConst(tr.From)
		}
		m.Transitions = append(m.Transitions, genTransition{
			EventConst: eventConst(tr.On),
			FromExpr:   from,
			ToExpr:     stateConst(tr.To),
		})
	}

	// Allowed actions grouped by state, in declared-state order.
	byState := map[string][]string{}
	for _, a := range d.Actions {
		for _, s := range a.AllowedStates {
			byState[s] = append(byState[s], actionConst(a.Name))
		}
	}
	for _, s := range d.States {
		if acts := byState[s]; len(acts) > 0 {
			m.AllowedByState = append(m.AllowedByState, genAllowed{
				StateConst: stateConst(s),
				ActionList: strings.Join(acts, ", "),
			})
		}
	}

	// Actions.
	for _, a := range d.Actions {
		risk, _ := riskExpr(a.Risk)
		appr, _ := approvalExpr(a.Approval)
		ga := genAction{
			Name:                    a.Name,
			Const:                   actionConst(a.Name),
			Func:                    lowerFirst(pascal(a.Name)) + "Contract",
			AllowedStatesList:       joinConsts(a.AllowedStates, stateConst),
			RequiredParametersList:  joinQuoted(a.RequiredParameters),
			RequiredPermissionsList: joinConsts(a.RequiredPermissions, permConst),
			Risk:                    risk,
			Approval:                appr,
			RequiresApproval:        appr == "action.ApprovalRequired",
		}
		for _, e := range a.FollowUpEvents {
			ga.FollowUpEvents = append(ga.FollowUpEvents, eventConst(e))
		}
		m.Actions = append(m.Actions, ga)
	}

	// --- test model ---
	bootstrap, initial := bootstrapTransition(d)
	m.BootstrapEventConst = eventConst(bootstrap)
	m.InitialState = stateConst(initial)
	m.Steps, m.FinalState = computeHappyPath(d, initial)
	m.UsesApprovalImport = anyApprovalStep(m.Steps)
	m.ActionTests = buildActionTests(d)

	return m, nil
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func joinConsts(values []string, fn func(string) string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fn(v))
	}
	return strings.Join(out, ", ")
}

func joinQuoted(values []string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprintf("%q", v))
	}
	return strings.Join(out, ", ")
}

func paramsLiteral(params []string) string {
	out := make([]string, 0, len(params))
	for _, p := range params {
		out = append(out, fmt.Sprintf("%q: %q", p, "sample-"+p))
	}
	return strings.Join(out, ", ")
}

func assertUniqueConsts(m genModel) error {
	seen := map[string]string{} // const name -> source value
	check := func(name, value string) error {
		if prev, ok := seen[name]; ok && prev != value {
			return fmt.Errorf("descriptor: identifiers %q and %q both map to Go constant %q; rename one", prev, value, name)
		}
		seen[name] = value
		return nil
	}
	for _, c := range m.Events {
		if err := check(c.Name, c.Value); err != nil {
			return err
		}
	}
	for _, c := range m.States {
		if err := check(c.Name, c.Value); err != nil {
			return err
		}
	}
	for _, c := range m.Perms {
		if err := check(c.Name, c.Value); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapTransition(d scaffoldDescriptor) (event, initialState string) {
	for _, tr := range d.Transitions {
		if tr.From == "" {
			return tr.On, tr.To
		}
	}
	return "", ""
}

// computeHappyPath walks the state machine from the initial state, choosing at
// each step the first declared action that is allowed in the current state and
// emits a follow-up event that transitions out of it. It stops on a cycle or a
// dead end. The walk is fully deterministic (declared order).
func computeHappyPath(d scaffoldDescriptor, initial string) ([]genStep, string) {
	// index: from-state + event -> to-state
	type key struct{ from, on string }
	trans := map[key]string{}
	for _, tr := range d.Transitions {
		trans[key{tr.From, tr.On}] = tr.To
	}

	var steps []genStep
	current := initial
	visited := map[string]bool{}
	idx := 0
	for !visited[current] {
		visited[current] = true
		next, advanced := "", false
		var chosen descriptorAction
		for _, a := range d.Actions {
			if !contains(a.AllowedStates, current) {
				continue
			}
			for _, e := range a.FollowUpEvents {
				if to, ok := trans[key{current, e}]; ok {
					chosen, next, advanced = a, to, true
					break
				}
			}
			if advanced {
				break
			}
		}
		if !advanced {
			break
		}
		steps = append(steps, genStep{
			ActionConst:      actionConst(chosen.Name),
			ActionName:       chosen.Name,
			FromState:        stateConst(current),
			ToState:          stateConst(next),
			RequiresApproval: strings.EqualFold(chosen.Approval, "required"),
			ParamsLiteral:    paramsLiteral(chosen.RequiredParameters),
			Index:            idx,
		})
		idx++
		current = next
	}

	final := stateConst(initial)
	if len(steps) > 0 {
		final = steps[len(steps)-1].ToState
	}
	return steps, final
}

func anyApprovalStep(steps []genStep) bool {
	for _, s := range steps {
		if s.RequiresApproval {
			return true
		}
	}
	return false
}

func buildActionTests(d scaffoldDescriptor) []genActionTest {
	stateSet := toSet(d.States)
	var tests []genActionTest
	for _, a := range d.Actions {
		allowed := toSet(a.AllowedStates)
		wrong := `"NO_SUCH_STATE"`
		for _, s := range d.States {
			if !allowed[s] {
				wrong = stateConst(s)
				break
			}
		}
		_ = stateSet
		tests = append(tests, genActionTest{
			Pascal:           pascal(a.Name),
			Const:            actionConst(a.Name),
			Func:             lowerFirst(pascal(a.Name)) + "Contract",
			AllowedState:     stateConst(a.AllowedStates[0]),
			WrongStateExpr:   wrong,
			RequiresApproval: strings.EqualFold(a.Approval, "required"),
			ParamsLiteral:    paramsLiteral(a.RequiredParameters),
		})
	}
	return tests
}

func contains(items []string, needle string) bool {
	for _, i := range items {
		if i == needle {
			return true
		}
	}
	return false
}

// --- rendering -------------------------------------------------------------

const domainGoTmpl = `// Code generated by 'kiff scaffold'; DO NOT EDIT the wiring by hand, but DO
// fill in the executor bodies.
//
// Package domain models the {{.Domain}} domain: a {{.Entity}} lifecycle with a
// state machine, action contracts, and a permission policy, assembled with
// domain.Builder. Executor bodies are TODO stubs — replace them with your real
// business logic. The shape mirrors the KIFF starter convention.
package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/actor"
	"github.com/kiff/kiff/pkg/kiff/adapter"
	kiffdomain "github.com/kiff/kiff/pkg/kiff/domain"
	"github.com/kiff/kiff/pkg/kiff/event"
	"github.com/kiff/kiff/pkg/kiff/permission"
	"github.com/kiff/kiff/pkg/kiff/runtime"
	"github.com/kiff/kiff/pkg/kiff/state"
	"github.com/kiff/kiff/pkg/kiff/store"
)

// Identifiers. KIFF convention: UPPER_SNAKE_CASE for events, states, and
// action names; dotted lowercase for permissions.
const (
	{{.AdapterConst}} = "{{.Adapter}}"

	{{.EntityConst}} = "{{.Entity}}"

{{range .Events}}	{{.Name}} = "{{.Value}}"
{{end}}
{{range .States}}	{{.Name}} = "{{.Value}}"
{{end}}
{{range .Actions}}	{{.Const}} = "{{.Name}}"
{{end}}
{{range .Perms}}	{{.Name}} permission.Permission = "{{.Value}}"
{{end}})

// Demo actors. Real applications source these from their identity layer.
var (
	SystemActor   = actor.Actor{ID: "system", Type: actor.TypeSystem, DisplayName: "System", Roles: []string{"system"}}
	AgentActor    = actor.Actor{ID: "{{.Domain}}-agent", Type: actor.TypeAgent, DisplayName: "{{.Entity}} Agent", Roles: []string{"agent"}}
	OperatorActor = actor.Actor{ID: "{{.Domain}}-operator", Type: actor.TypeHuman, DisplayName: "{{.Entity}} Operator", Roles: []string{"operator"}}
)

// NewStateMachine returns the {{.Domain}} state machine.
func NewStateMachine() *state.TransitionMachine {
	machine := state.NewTransitionMachine(
{{range .Transitions}}		state.Transition{EventType: {{.EventConst}}, From: {{.FromExpr}}, To: {{.ToExpr}}},
{{end}}	)
{{range .AllowedByState}}	machine.SetAllowedActions({{.StateConst}}, []string{ {{.ActionList}} })
{{end}}	return machine
}

// NewPermissionPolicy returns the demo permission policy.
//
// TODO: tighten role assignment. The scaffolder assigns every declared role to
// each actor so the generated tests pass out of the box. Replace this with your
// real identity model so authority is least-privilege.
func NewPermissionPolicy() *permission.SimplePolicy {
	policy := permission.NewSimplePolicy()
{{range $role := .Roles}}{{range $role.PermConsts}}	policy.GrantRole("{{$role.Name}}", {{.}})
{{end}}{{end}}{{range .Roles}}	policy.AssignRole(AgentActor.ID, "{{.Name}}")
	policy.AssignRole(OperatorActor.ID, "{{.Name}}")
	policy.AssignRole(SystemActor.ID, "{{.Name}}")
{{end}}	return policy
}

// Contracts returns the action contracts owned by this domain.
func Contracts() []action.ActionContract {
	return []action.ActionContract{ {{range $i, $a := .Actions}}{{if $i}}, {{end}}{{$a.Func}}(){{end}} }
}

{{range .Actions}}
func {{.Func}}() action.ActionContract {
	return action.ActionContract{
		Name:                {{.Const}},
		AllowedStates:       []string{ {{.AllowedStatesList}} },
		RequiredParameters:  []string{ {{.RequiredParametersList}} },
		RequiredPermissions: []permission.Permission{ {{.RequiredPermissionsList}} },
		Risk:                {{.Risk}},
		ApprovalRequirement: {{.Approval}},
		Executor: func(_ context.Context, ctx action.ActionContext) (action.ActionResult, error) {
			// TODO: implement {{.Name}}. Replace this stub with real business logic.
			return action.ActionResult{
				ActionName:     {{.Const}},
				EntityID:       ctx.EntityID,
				Status:         action.ExecutionSucceeded,
				Executed:       true,
				Message:        "TODO: {{.Name}} executed",
				EffectsSummary: "TODO: describe the effect of {{.Name}}",
{{if .FollowUpEvents}}				FollowUpEvents: []event.Event{
{{range .FollowUpEvents}}					newEvent(ctx.EntityID, {{.}}, ctx.Actor.ID, ctx.Parameters),
{{end}}				},
{{end}}				ExecutedAt: time.Now().UTC(),
			}, nil
		},
	}
}
{{end}}

// NewDefinition returns the domain definition assembled via domain.Builder.
func NewDefinition() (kiffdomain.Definition, error) {
	b := kiffdomain.New("{{.Domain}}")
	b = b.Entity({{.EntityConst}})
{{range .Events}}	b = b.Event({{.Name}})
{{end}}{{range .Transitions}}	b = b.Transition({{.EventConst}}, {{.FromExpr}}, {{.ToExpr}})
{{end}}{{range .AllowedByState}}	b = b.Allow({{.StateConst}}, {{.ActionList}})
{{end}}	for _, contract := range Contracts() {
		b = b.Action(contract)
	}
	return b.Build()
}

// NewInputAdapter returns the domain input adapter.
func NewInputAdapter() (adapter.Adapter, error) {
	return adapter.NewPassthroughAdapter({{.AdapterConst}})
}

// NewRuntime returns a runtime wired for this domain using in-memory stores.
func NewRuntime() (*runtime.Runtime, error) {
	return NewRuntimeWithStores(nil)
}

// NewRuntimeWithStores returns a runtime wired for this domain using the
// provided store bundle. A nil bundle falls back to in-memory stores.
func NewRuntimeWithStores(stores *store.Bundle) (*runtime.Runtime, error) {
	def, err := NewDefinition()
	if err != nil {
		return nil, err
	}
	in, err := NewInputAdapter()
	if err != nil {
		return nil, err
	}
	return runtime.NewForDomain(def, runtime.Config{
		PermissionPolicy: NewPermissionPolicy(),
		Adapters:         []adapter.Adapter{in},
		Stores:           stores,
	})
}

func newEvent(entityID, eventType, actorID string, payload map[string]any) event.Event {
	return event.Event{
		ID:         fmt.Sprintf("evt-%s-%s-%d", eventType, entityID, time.Now().UnixNano()),
		Type:       eventType,
		EntityID:   entityID,
		EntityType: {{.EntityConst}},
		Source:     "{{.Domain}}/executor",
		ActorID:    actorID,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	}
}
`

const domainTestGoTmpl = `// Code generated by 'kiff scaffold'.
package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/adapter"
{{if .UsesApprovalImport}}	"github.com/kiff/kiff/pkg/kiff/approval"
{{end}})

// TestLoop_HappyPath drives the {{.Entity}} through its state machine, granting
// approvals where required, and asserts the final state.
func TestLoop_HappyPath(t *testing.T) {
	ctx := context.Background()
	rt, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	const entityID = "{{.Domain}}-1"

	if _, err := rt.IngestRaw(ctx, adapter.RawInput{
		ID:         "evt-bootstrap",
		Adapter:    {{.AdapterConst}},
		Type:       {{.BootstrapEventConst}},
		Source:     "{{.Domain}}/test",
		EntityID:   entityID,
		EntityType: {{.EntityConst}},
		ActorID:    SystemActor.ID,
		ReceivedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("IngestRaw(bootstrap): %v", err)
	}
{{range .Steps}}
	// Step {{.Index}}: {{.ActionName}} ({{.FromState}} -> {{.ToState}})
	{
		contract, ok := rt.Actions.Get({{.ActionConst}})
		if !ok {
			t.Fatalf("missing contract {{.ActionName}}")
		}
		actionCtx := action.ActionContext{
			ActionName:   {{.ActionConst}},
			EntityID:     entityID,
			EntityType:   {{$.EntityConst}},
			CurrentState: {{.FromState}},
			Actor:        AgentActor,
			Parameters:   map[string]any{ {{.ParamsLiteral}} },
{{if .RequiresApproval}}			ApprovalID:   "approval-{{.Index}}",
{{end}}		}
{{if .RequiresApproval}}		if _, err := rt.RequestApproval(ctx, actionCtx.ApprovalID, actionCtx, contract, "scaffold happy path"); err != nil {
			t.Fatalf("RequestApproval({{.ActionName}}): %v", err)
		}
		if _, err := rt.ReviewApproval(ctx, actionCtx.ApprovalID, OperatorActor.ID, approval.StatusGranted, "approved"); err != nil {
			t.Fatalf("ReviewApproval({{.ActionName}}): %v", err)
		}
{{end}}		result, err := rt.ExecuteAction(ctx, actionCtx, contract)
		if err != nil {
			t.Fatalf("ExecuteAction({{.ActionName}}): %v", err)
		}
		if result.Status != action.ExecutionSucceeded {
			t.Fatalf("{{.ActionName}}: expected succeeded, got %s", result.Status)
		}
	}
{{end}}
	current, _, err := rt.States.Current(ctx, entityID)
	if err != nil {
		t.Fatalf("States.Current: %v", err)
	}
	if current.Value != {{.FinalState}} {
		t.Fatalf("expected final state %q, got %q", {{.FinalState}}, current.Value)
	}
}
{{range .ActionTests}}
func Test{{.Pascal}}_AllowedState(t *testing.T) {
	policy := NewPermissionPolicy()
	actionCtx := action.ActionContext{
		ActionName:   {{.Const}},
		EntityID:     "entity-allowed",
		EntityType:   {{$.EntityConst}},
		CurrentState: {{.AllowedState}},
		Actor:        AgentActor,
		Parameters:   map[string]any{ {{.ParamsLiteral}} },
	}
	_, err := action.DefaultValidator{}.Validate(context.Background(), actionCtx, {{.Func}}(), policy)
{{if .RequiresApproval}}	// Allowed in this state, but the approval gate must hold until granted.
	if !errors.Is(err, action.ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired in allowed state, got %v", err)
	}
{{else}}	if err != nil {
		t.Fatalf("expected validation to pass in allowed state, got %v", err)
	}
{{end}}}

func Test{{.Pascal}}_BlockedFromWrongState(t *testing.T) {
	policy := NewPermissionPolicy()
	actionCtx := action.ActionContext{
		ActionName:   {{.Const}},
		EntityID:     "entity-blocked",
		EntityType:   {{$.EntityConst}},
		CurrentState: {{.WrongStateExpr}},
		Actor:        AgentActor,
		Parameters:   map[string]any{ {{.ParamsLiteral}} },
	}
	_, err := action.DefaultValidator{}.Validate(context.Background(), actionCtx, {{.Func}}(), policy)
	if !errors.Is(err, action.ErrStateNotAllowed) {
		t.Fatalf("expected ErrStateNotAllowed from wrong state, got %v", err)
	}
}
{{end}}`

func renderTemplate(name, tmpl string, m genModel) ([]byte, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, m); err != nil {
		return nil, err
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated %s: %w\n----\n%s", name, err, buf.String())
	}
	return formatted, nil
}

func generateDomainGo(d scaffoldDescriptor) ([]byte, error) {
	m, err := buildModel(d)
	if err != nil {
		return nil, err
	}
	return renderTemplate("domain.go", domainGoTmpl, m)
}

func generateDomainTestGo(d scaffoldDescriptor) ([]byte, error) {
	m, err := buildModel(d)
	if err != nil {
		return nil, err
	}
	return renderTemplate("domain_test.go", domainTestGoTmpl, m)
}

// --- command ---------------------------------------------------------------

func runScaffold(args []string) error {
	fs := flag.NewFlagSet("kiff scaffold", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "USAGE:")
		fmt.Fprintln(os.Stderr, "  kiff scaffold <module-path> -descriptor <file|-> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Scaffold a KIFF project (or just a domain/ package) from a JSON")
		fmt.Fprintln(os.Stderr, "domain descriptor. The descriptor is a code-generation seed only; the")
		fmt.Fprintln(os.Stderr, "runtime still builds domains via domain.Builder. Executor bodies are")
		fmt.Fprintln(os.Stderr, "emitted as TODO stubs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "FLAGS:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "EXAMPLES (flags must precede the module path):")
		fmt.Fprintln(os.Stderr, "  kiff scaffold -descriptor order.json github.com/acme/orders")
		fmt.Fprintln(os.Stderr, "  cat order.json | kiff scaffold -descriptor - github.com/acme/orders")
		fmt.Fprintln(os.Stderr, "  kiff scaffold -descriptor order.json -domain-only -dir . github.com/acme/orders")
	}
	descriptorPath := fs.String("descriptor", "", "path to a JSON scaffold descriptor, or '-' to read stdin (required)")
	dir := fs.String("dir", "", "directory to scaffold into (default: last segment of module path)")
	force := fs.Bool("force", false, "scaffold into a non-empty directory")
	replaceLocal := fs.String("replace-local", "", "emit a `replace github.com/kiff/kiff => <path>` directive in go.mod")
	domainOnly := fs.Bool("domain-only", false, "emit only the domain/ package (skip the project shell)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one argument: the module path")
	}
	if strings.TrimSpace(*descriptorPath) == "" {
		fs.Usage()
		return errors.New("the -descriptor flag is required")
	}

	modulePath := strings.TrimSpace(fs.Arg(0))
	if err := validateModulePath(modulePath); err != nil {
		return err
	}
	moduleName := path.Base(modulePath)

	descriptor, err := readDescriptor(*descriptorPath)
	if err != nil {
		return err
	}

	domainGo, err := generateDomainGo(descriptor)
	if err != nil {
		return err
	}
	domainTestGo, err := generateDomainTestGo(descriptor)
	if err != nil {
		return err
	}

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

	if !*domainOnly {
		tmpl, err := resolveTemplate(templateStarter)
		if err != nil {
			return err
		}
		data := templateData{
			ModulePath:   modulePath,
			ModuleName:   moduleName,
			GoVersion:    StarterGoVersion,
			KiffVersion:  StarterKiffVersion,
			ReplaceLocal: strings.TrimSpace(*replaceLocal),
		}
		if err := scaffold(target, tmpl, data); err != nil {
			return err
		}
	}

	domainDir := filepath.Join(target, "domain")
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(domainDir, "domain.go"), domainGo, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(domainDir, "domain_test.go"), domainTestGo, 0o644); err != nil {
		return err
	}

	fmt.Println("scaffolded KIFF domain")
	fmt.Printf("  module : %s\n", modulePath)
	fmt.Printf("  domain : %s (%s)\n", descriptor.Domain, descriptor.Entity)
	fmt.Printf("  path   : %s\n", target)
	if *domainOnly {
		fmt.Println("  scope  : domain-only (domain/domain.go + domain/domain_test.go)")
	} else {
		fmt.Println("  scope  : full project (starter shell + generated domain/)")
	}
	fmt.Println("")
	fmt.Println("next steps:")
	rel, _ := filepath.Rel(mustGetwd(), target)
	if rel == "" || strings.HasPrefix(rel, "..") {
		rel = target
	}
	fmt.Printf("  cd %s\n", rel)
	fmt.Println("  go mod tidy")
	fmt.Println("  go test ./...        # generated tests pass; executor bodies are TODO stubs")
	if !*domainOnly {
		fmt.Println("  go run ./cmd/server")
	}
	return nil
}

func readDescriptor(pathOrDash string) (scaffoldDescriptor, error) {
	if pathOrDash == "-" {
		return parseDescriptor(os.Stdin)
	}
	f, err := os.Open(pathOrDash)
	if err != nil {
		return scaffoldDescriptor{}, fmt.Errorf("open descriptor: %w", err)
	}
	defer f.Close()
	return parseDescriptor(f)
}
