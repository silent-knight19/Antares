// Command antares is the Redis-compatible server (docs/PLAN.md §3.1).
//
// P0.1 scaffold: the listener arrives in P1.1 (docs/PLAN.md §5).
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
		fmt.Printf("antares %s\n", version)
		return
	}

	fmt.Fprintln(os.Stderr, "antares: not implemented yet (P0.1 scaffold) — see docs/PLAN.md §5 P1.1")
	os.Exit(1)
}
