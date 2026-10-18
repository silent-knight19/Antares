// Command observatory is the Antares Observatory bridge: REST + SSE over the
// event tap, serving the SPA (docs/PLAN.md §3.2, ADR-013).
//
// P0.1 scaffold: the bridge arrives in U2 (docs/PLAN.md §6).
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("observatory %s\n", version)
		return
	}

	fmt.Fprintln(os.Stderr, "observatory: not implemented yet (P0.1 scaffold) — see docs/PLAN.md §6 U2")
	os.Exit(1)
}
