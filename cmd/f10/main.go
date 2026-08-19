// Command f10 is the read-only lookaround binary for f10 configuration: it
// prints the effective configuration of the repo it runs in, with a
// provenance per value. It writes nothing, anywhere - bin/resolve.sh stays
// the agent-facing surface; this explains to humans what the resolver hands
// to agents.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

const version = "0.1.0"

func main() {
	app := &cli.Command{
		Name:     "f10",
		Usage:    "look around a project's f10 configuration",
		Version:  version,
		Commands: []*cli.Command{configCommand()},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "f10:", err)
		os.Exit(1)
	}
}
