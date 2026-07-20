package main

// CLIVersion is the kiff CLI version. The framework version pinned in
// generated go.mod files is tracked separately in StarterKiffVersion so the
// two can move independently.
const (
	CLIVersion         = "0.8.0"
	StarterGoVersion   = "1.22"
	StarterKiffVersion = "v0.7.0"
)

func versionString() string {
	return "kiff " + CLIVersion
}
