package main

// CLIVersion is the kiff CLI version. The framework version pinned in
// generated go.mod files is tracked separately in StarterKiffVersion so the
// two can move independently.
//
// Both are bumped as part of preparing a release. They were not, through
// v0.9.0: the CLI installed from @latest reported 0.8.0, and a scaffolded
// project pinned v0.7.0. The second was invisible because `go mod tidy`
// upgrades anyway — the generated code imports pkg/kiff/limit, which v0.7.0
// does not have — so the quickstart worked by accident rather than by
// construction. A version_test pins both against the changelog now.
const (
	CLIVersion         = "0.9.1"
	StarterGoVersion   = "1.22"
	StarterKiffVersion = "v0.9.1"
)

func versionString() string {
	return "kiff " + CLIVersion
}
