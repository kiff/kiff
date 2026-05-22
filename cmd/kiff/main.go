// Command kiff is the KIFF framework's developer CLI.
//
// Today it has one subcommand:
//
//	kiff new <module-path>     Scaffold a new KIFF project at <module-path>
//	                            into a local directory derived from the path.
//
// Example:
//
//	kiff new github.com/acme/orders
//
// This produces ./orders/ with a runnable HTTP server, a tasks-style starter
// domain you can rename to fit your own vocabulary, and a go.mod that depends
// on the published KIFF framework module.
package main

import (
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
	fmt.Fprintln(w, "  kiff version                     Print CLI version")
	fmt.Fprintln(w, "  kiff help                        Show this message")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "RUN 'kiff new -h' FOR FLAGS.")
}
