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

	if err := cli.ShowRootCommandHelp(root); err != nil {
		return err
	}

	if cmd.Bool("all") {
		writeNestedPlumbing(root.Writer, root)
	}

	return nil
}

// writeNestedPlumbing lists the plumbing below the top level by full path:
// the root listing shows one level, and urfave groups a subcommand's rows by
// category only once that command runs.
func writeNestedPlumbing(w io.Writer, root *cli.Command) {
	var rows [][2]string

	for _, top := range root.Commands {
		var walk func(path string, c *cli.Command)
		walk = func(path string, c *cli.Command) {
			for _, sub := range c.Commands {
				p := path + " " + sub.Name
				// showPlumbing already cleared Hidden, so the category alone marks it
				if sub.Category == plumbingCategory {
					rows = append(rows, [2]string{p, sub.Usage})
				}

				walk(p, sub)
			}
		}

		walk(top.Name, top)
	}

	if len(rows) == 0 {
		return
	}

	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}

	fmt.Fprintln(w, "\nPLUMBING SUBCOMMANDS:")

	for _, r := range rows {
		fmt.Fprintf(w, "   %-*s  %s\n", width, r[0], strings.Join(strings.Fields(r[1]), " "))
	}
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
		required, optional := flagNames(c)
		parts = append(parts, required...)

		if len(optional) > 0 {
			parts = append(parts, "["+strings.Join(optional, " ")+"]")
		}
	}

	return strings.Join(parts, " ") + " - " + usage
}

// flagNames splits the visible flags into required and optional, each with
// its aliases (--description|-m), so the reference spells a flag the way the
// steps do and never brackets one a run cannot omit.
func flagNames(c *cli.Command) ([]string, []string) {
	var required, optional []string

	for _, f := range c.Flags {
		if v, ok := f.(cli.VisibleFlag); ok && !v.IsVisible() {
			continue
		}

		var names []string

		for _, n := range f.Names() {
			if len(n) == 1 {
				names = append(names, "-"+n)
			} else {
				names = append(names, "--"+n)
			}
		}

		if r, ok := f.(cli.RequiredFlag); ok && r.IsRequired() {
			required = append(required, strings.Join(names, "|"))
		} else {
			optional = append(optional, strings.Join(names, "|"))
		}
	}

	return required, optional
}
