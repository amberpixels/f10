package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"
)

// plumbingCategory marks a verb the steps and skills call: Hidden from
// `f10 --help`, listed under this heading by `f10 help --all`. A Hidden
// command with no category is internal (hooks, the daemon) and never listed.
const plumbingCategory = "plumbing"

func isPlumbing(c *cli.Command) bool { return c.Hidden && c.Category == plumbingCategory }

func isInternal(c *cli.Command) bool { return c.Hidden && c.Category != plumbingCategory }

// helpCommand replaces urfave's own, which it adds only when the root has no
// `help`; -h and --help still go through urfave's flag.
func helpCommand() *cli.Command {
	return &cli.Command{
		Name:      "help",
		Aliases:   []string{"h"},
		Usage:     "list the commands, or show help for one; --all adds the plumbing the steps call",
		ArgsUsage: "[command]",
		HideHelp:  true,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "also list the plumbing commands the steps and skills call"},
			&cli.BoolFlag{Name: "agent", Usage: "print the one-line-per-command reference agents read"},
		},
		Action: runHelp,
	}
}

func runHelp(ctx context.Context, cmd *cli.Command) error {
	root := cmd.Root()

	if cmd.Bool("agent") {
		writeAgentReference(root.Writer, newApp())
		return nil
	}

	if cmd.Bool("all") {
		showPlumbing(root)
	}

	if name := cmd.Args().First(); name != "" {
		return cli.ShowCommandHelp(ctx, root, name)
	}

	return cli.ShowRootCommandHelp(root)
}

// showPlumbing clears Hidden on every plumbing command in the tree; urfave
// then prints them under their category, below the uncategorised ones.
func showPlumbing(c *cli.Command) {
	for _, sub := range c.Commands {
		if isPlumbing(sub) {
			sub.Hidden = false
		}

		showPlumbing(sub)
	}
}

// writeAgentReference prints one line per runnable command, internal ones
// left out, generated from the definitions so it never names a missing verb.
func writeAgentReference(w io.Writer, root *cli.Command) {
	fmt.Fprintf(w, "f10 %s commands, one per line: <command> <args> [flags] - what it does\n", root.Version)

	var walk func(path string, c *cli.Command)
	walk = func(path string, c *cli.Command) {
		for _, sub := range c.Commands {
			if isInternal(sub) || sub.Name == "help" {
				continue
			}

			p := path + " " + sub.Name
			// a noun that only groups its subcommands runs nothing itself
			if sub.Action != nil {
				fmt.Fprintln(w, referenceLine(p, sub))
			}

			walk(p, sub)
		}
	}

	walk("f10", root)
}

func referenceLine(path string, c *cli.Command) string {
	// a multi-line ArgsUsage or Usage would split the command over lines
	args := strings.Join(strings.Fields(c.ArgsUsage), " ")
	usage := strings.Join(strings.Fields(c.Usage), " ")

	parts := []string{path}
	if args != "" {
		parts = append(parts, args)
	}

	// an ArgsUsage that spells out its flags already says more than their names
	if !strings.Contains(args, "--") {
		if flags := flagNames(c); len(flags) > 0 {
			parts = append(parts, "["+strings.Join(flags, " ")+"]")
		}
	}

	return strings.Join(parts, " ") + " - " + usage
}

func flagNames(c *cli.Command) []string {
	var names []string

	for _, f := range c.Flags {
		if v, ok := f.(cli.VisibleFlag); ok && !v.IsVisible() {
			continue
		}

		name := f.Names()[0]
		if len(name) == 1 {
			names = append(names, "-"+name)
		} else {
			names = append(names, "--"+name)
		}
	}

	return names
}
