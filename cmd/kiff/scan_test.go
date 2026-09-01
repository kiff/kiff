package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGoScanFile(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func TestScanGoPathDirectiveFindsUnguardedSink(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

//kiff:tool
func DropProduction(db Database, name string) error {
	return db.DropDatabase(name)
}
`)
	report, err := scanGoPath(dir, scanOptions{})
	if err != nil {
		t.Fatalf("scanGoPath: %v", err)
	}
	if report.Summary.Tools != 1 || report.Summary.ReviewRequired != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	finding := report.Findings[0]
	if finding.Tool != "DropProduction" || finding.Severity != scanHigh {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	if finding.File != "agent.go" || finding.Line != 5 {
		t.Fatalf("unexpected location: %+v", finding)
	}
}

func TestScanGoPathRecognizesRegisteredTool(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

func RefundOrder(g Gateway, id string) error {
	return g.Refund(id)
}

func Register(reg Registry) {
	reg.RegisterTool("refund_order", RefundOrder)
}
`)
	report, err := scanGoPath(dir, scanOptions{})
	if err != nil {
		t.Fatalf("scanGoPath: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Tool != "RefundOrder" {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestScanGoPathCreditsGuardOnlyBeforeSink(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

//kiff:tool
func Guarded(g Guard, db Database, name string) error {
	if err := g.Decide(name); err != nil { return err }
	return db.DropDatabase(name)
}

//kiff:tool
func TooLate(g Guard, db Database, name string) error {
	err := db.DropDatabase(name)
	_ = g.Decide(name)
	return err
}
`)
	report, err := scanGoPath(dir, scanOptions{})
	if err != nil {
		t.Fatalf("scanGoPath: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Tool != "TooLate" {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestScanGoPathIgnoresConsequentialNonTool(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

func InternalCleanup(db Database, name string) error {
	return db.DropDatabase(name)
}
`)
	report, err := scanGoPath(dir, scanOptions{})
	if err != nil {
		t.Fatalf("scanGoPath: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("unexpected findings: %+v", report.Findings)
	}
}

func TestRunScanJSONAndThreshold(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

func SendNotice(m Mailer) error { return m.SendEmail("hello") }
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runScanWithIO(&stdout, &stderr, []string{
		"-format", "json", "-tool", "SendNotice", "-fail-on", "medium", dir,
	})
	if !errors.Is(err, errScanFindings) {
		t.Fatalf("expected threshold failure, got %v; stderr=%s", err, stderr.String())
	}
	var report goScanReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if report.Summary.Medium != 1 || report.Findings[0].Category != "external communication" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunScanSARIF(t *testing.T) {
	dir := writeGoScanFile(t, `package agent

//kiff:tool
func RunShell(shell Shell) error { return shell.RunCommand("deploy") }
`)
	var stdout bytes.Buffer
	err := runScanWithIO(&stdout, &bytes.Buffer{}, []string{
		"-format", "sarif", "-fail-on", "none", dir,
	})
	if err != nil {
		t.Fatalf("runScanWithIO: %v", err)
	}
	var sarif map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &sarif); err != nil {
		t.Fatalf("decode SARIF: %v", err)
	}
	if sarif["version"] != "2.1.0" {
		t.Fatalf("unexpected SARIF version: %+v", sarif["version"])
	}
	if !strings.Contains(stdout.String(), "KIFF-GO-001") {
		t.Fatalf("SARIF omitted rule: %s", stdout.String())
	}
}
