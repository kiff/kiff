package main

import (
	"flag"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden files when set: go test ./cmd/kiff -run Golden -update
var update = flag.Bool("update", false, "update scaffold golden files")

const descriptorPath = "testdata/scaffold/order-refund.json"

func loadDescriptor(t *testing.T) scaffoldDescriptor {
	t.Helper()
	f, err := os.Open(descriptorPath)
	if err != nil {
		t.Fatalf("open descriptor: %v", err)
	}
	defer f.Close()
	d, err := parseDescriptor(f)
	if err != nil {
		t.Fatalf("parseDescriptor: %v", err)
	}
	return d
}

// TestScaffoldGolden pins the generated domain package against reviewed golden
// files. A fixed descriptor must always produce the same output.
func TestScaffoldGolden(t *testing.T) {
	d := loadDescriptor(t)

	cases := []struct {
		name   string
		golden string
		gen    func(scaffoldDescriptor) ([]byte, error)
	}{
		{"domain.go", "testdata/scaffold/order-refund.domain.go.golden", generateDomainGo},
		{"domain_test.go", "testdata/scaffold/order-refund.domain_test.go.golden", generateDomainTestGo},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.gen(d)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if *update {
				if err := os.WriteFile(tc.golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Fatalf("generated %s does not match golden. Run: go test ./cmd/kiff -run Golden -update\n--- got ---\n%s", tc.name, got)
			}
		})
	}
}

// TestScaffoldGenerated_IsGofmt ensures generated code is always gofmt-clean.
func TestScaffoldGenerated_IsGofmt(t *testing.T) {
	d := loadDescriptor(t)
	for _, gen := range []func(scaffoldDescriptor) ([]byte, error){generateDomainGo, generateDomainTestGo} {
		got, err := gen(d)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		formatted, err := format.Source(got)
		if err != nil {
			t.Fatalf("generated code is not valid Go: %v", err)
		}
		if string(formatted) != string(got) {
			t.Fatalf("generated code is not gofmt-clean")
		}
	}
}

// TestScaffold_Deterministic verifies repeated generation is byte-identical.
func TestScaffold_Deterministic(t *testing.T) {
	d := loadDescriptor(t)
	a, err := generateDomainGo(d)
	if err != nil {
		t.Fatalf("generate a: %v", err)
	}
	b, err := generateDomainGo(d)
	if err != nil {
		t.Fatalf("generate b: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("generation is not deterministic")
	}
}

func TestParseDescriptor_Validation(t *testing.T) {
	cases := []struct {
		name string
		json string
		ok   bool
	}{
		{"valid minimal", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["S1"]}]}`, true},
		{"missing domain", `{"entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["S1"]}]}`, false},
		{"no bootstrap", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[],"actions":[{"name":"A","allowed_states":["S1"]}]}`, false},
		{"unknown transition state", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"NOPE"}],"actions":[{"name":"A","allowed_states":["S1"]}]}`, false},
		{"action allowed state unknown", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["X"]}]}`, false},
		{"bad risk", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["S1"],"risk":"nope"}]}`, false},
		{"bad approval", `{"domain":"d","entity":"E","events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["S1"],"approval":"maybe"}]}`, false},
		{"unknown field", `{"domain":"d","entity":"E","bogus":1,"events":["E1"],"states":["S1"],"transitions":[{"on":"E1","from":"","to":"S1"}],"actions":[{"name":"A","allowed_states":["S1"]}]}`, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDescriptor(strings.NewReader(tc.json))
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestPascalAndConsts(t *testing.T) {
	cases := map[string]string{
		"ORDER_PLACED":   "OrderPlaced",
		"refund.execute": "RefundExecute",
		"MARK_PAID":      "MarkPaid",
		"done":           "Done",
	}
	for in, want := range cases {
		if got := pascal(in); got != want {
			t.Fatalf("pascal(%q) = %q, want %q", in, got, want)
		}
	}
	if got := eventConst("ORDER_PAID"); got != "EventOrderPaid" {
		t.Fatalf("eventConst: %q", got)
	}
	if got := permConst("refund.execute"); got != "PermRefundExecute" {
		t.Fatalf("permConst: %q", got)
	}
}

// TestScaffold_DomainOnly checks the domain-only path writes exactly the
// domain package and no project shell.
func TestScaffold_DomainOnly(t *testing.T) {
	tmp := t.TempDir()
	descAbs, err := filepath.Abs(descriptorPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	args := []string{"-descriptor", descAbs, "-domain-only", "-dir", tmp, "-force", "github.com/acme/orders"}
	if err := runScaffold(args); err != nil {
		t.Fatalf("runScaffold: %v", err)
	}
	for _, f := range []string{"domain/domain.go", "domain/domain_test.go"} {
		if _, err := os.Stat(filepath.Join(tmp, f)); err != nil {
			t.Fatalf("expected %s: %v", f, err)
		}
	}
	// No project shell in domain-only mode.
	if _, err := os.Stat(filepath.Join(tmp, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("expected no go.mod in domain-only mode, got %v", err)
	}
}

// TestScaffold_FullProjectLayout checks the full project reuses the starter
// shell and overlays the generated domain.
func TestScaffold_FullProjectLayout(t *testing.T) {
	tmp := t.TempDir()
	descAbs, err := filepath.Abs(descriptorPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	args := []string{"-descriptor", descAbs, "-dir", tmp, "-force", "github.com/acme/orders"}
	if err := runScaffold(args); err != nil {
		t.Fatalf("runScaffold: %v", err)
	}
	for _, f := range []string{"go.mod", "README.md", "cmd/server/main.go", "domain/domain.go", "domain/domain_test.go"} {
		if _, err := os.Stat(filepath.Join(tmp, f)); err != nil {
			t.Fatalf("expected %s: %v", f, err)
		}
	}
	// The domain must be the generated one (refund), not the starter tasks.
	domainGo := readFile(t, filepath.Join(tmp, "domain", "domain.go"))
	if !strings.Contains(domainGo, "ActionRefundOrder") {
		t.Fatalf("domain.go is not the generated refund domain:\n%s", domainGo)
	}
	// Server main must import the user's domain package.
	mainGo := readFile(t, filepath.Join(tmp, "cmd", "server", "main.go"))
	if !strings.Contains(mainGo, `"github.com/acme/orders/domain"`) {
		t.Fatalf("main.go did not rewrite domain import:\n%s", mainGo)
	}
}
