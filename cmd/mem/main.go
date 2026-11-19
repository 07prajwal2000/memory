// Command mem is a local long-term memory store for AI agents.
package main

import (
	"os"

	"memory/internal/cli"
	"memory/internal/setup"
)

func main() {
	setup.SetVersion(cli.Version)
	os.Exit(cli.Run(os.Args[1:], cli.DefaultIO(), cli.DefaultEnv()))
}
