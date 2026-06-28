// Command lotsman is the operator CLI (scaffold). It will grow cobra-based
// subcommands for managing clusters/agents and running
// investigations from the terminal.
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "version":
		fmt.Printf("lotsman %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `lotsman — Kubernetes monitoring & investigation CLI (scaffold)

Usage:
  lotsman <command>

Commands:
  version   Print the version
`)
}
