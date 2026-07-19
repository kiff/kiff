// Command kiff is the KIFF framework's developer CLI.
//
// Subcommands:
//
//	kiff new <module-path>      Scaffold a new KIFF project around the fixed
//	                            starter (tasks) domain.
//	kiff scaffold <module-path> Scaffold a project (or just a domain/ package)
//	    -descriptor <file|->    from a JSON domain descriptor — a code-generation
//	                            seed that emits a framework-faithful domain with
//	                            TODO executor stubs and passing tests.
//
// Example:
//
//	kiff new github.com/acme/orders
//	kiff scaffold -descriptor order.json github.com/acme/orders
//
// This produces ./orders/ with a runnable HTTP server, a domain package, and a
// go.mod that depends on the published KIFF framework module.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "new":
		if err := runNew(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kiff new: %v\n", err)
			os.Exit(1)
		}
	case "scaffold":
		if err := runScaffold(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kiff scaffold: %v\n", err)
			os.Exit(1)
		}
	case "verify":
		if err := runVerify(os.Args[2:]); err != nil {
			if errors.Is(err, errVerifyFailed) {
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "kiff verify: %v\n", err)
			os.Exit(1)
		}
	case "timeline":
		if err := runTimeline(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kiff timeline: %v\n", err)
			os.Exit(1)
		}
	case "apply":
		if err := runApply(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kiff apply: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(versionString())
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "kiff: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "kiff — developer CLI for the KIFF framework")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "USAGE:")
	fmt.Fprintln(w, "  kiff new <module-path> [flags]   Scaffold a new KIFF project")
	fmt.Fprintln(w, "  kiff scaffold <module-path>      Scaffold a project (or domain/ package)")
	fmt.Fprintln(w, "    -descriptor <file|->           from a JSON domain descriptor")
	fmt.Fprintln(w, "  kiff verify [path]               Structurally verify a domain package")
	fmt.Fprintln(w, "  kiff timeline -entity <id>       Render the audit timeline")
	fmt.Fprintln(w, "                                   from a running httpapi server")
	fmt.Fprintln(w, "  kiff apply [-f kiff.yaml]         Push a domain contract to a KIFF cloud")
	fmt.Fprintln(w, "                                   (endpoint via -endpoint / KIFF_CLOUD_URL)")
	fmt.Fprintln(w, "  kiff version                     Print CLI version")
	fmt.Fprintln(w, "  kiff help                        Show this message")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "RUN 'kiff <subcommand> -h' FOR FLAGS.")
}
