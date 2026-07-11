// Command lotsman is the operator CLI: a cobra-based client for the Lotsman
// control-plane REST API. It runs investigations and inspects incidents and
// clusters from the terminal (the same surface the UI drives), so operators no
// longer need raw curl.
package main

import (
	"fmt"
	"os"
)

// version is stamped at build time via -ldflags "-X main.version=...".
// (See the Makefile / Dockerfile: -X main.version=$(VERSION).)
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Errors are already routed to stderr with a non-zero exit; keep the
		// message terse and consistent. Usage is silenced on runtime errors
		// (SilenceUsage/SilenceErrors on the root command).
		fmt.Fprintln(os.Stderr, "lotsman: "+err.Error())
		os.Exit(1)
	}
}
