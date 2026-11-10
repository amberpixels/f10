package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/doctor"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check the plugin, the binary and the environment they run in; changes nothing",
		Flags: []cli.Flag{
			ui.JSONFlag("emit one object per check"),
		},
		Action: runDoctor,
	}
}

func runDoctor(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding the home directory: %w", err)
	}

	results := doctor.Run(ctx, &doctor.Env{
		ClaudeDir:   filepath.Dir(runplan.ClaudeSettingsPath()),
		Home:        home,
		Layout:      t.Layout,
		Probes:      t.Probes,
		Facts:       t.Effective,
		Version:     buildinfo.Version(),
		Driver:      t.Driver(),
		NeedsDriver: !t.HostIssues(),
		Getenv:      os.Getenv,
		LookPath:    exec.LookPath,
		Run:         shell.Capture,
	})

	if cmd.Bool("json") {
		if err := ui.JSON(cmd.Writer, results); err != nil {
			return err
		}
	} else {
		renderDoctor(cmd.Writer, results)
	}

	// the table already says what failed; the exit code is for scripts
	if doctor.Failed(results) {
		return cli.Exit("", 1)
	}

	return nil
}

func renderDoctor(w io.Writer, results []doctor.Result) {
	faint := lipgloss.NewStyle().Faint(true)

	nameW, statusW := 0, 0
	for _, r := range results {
		nameW = max(nameW, len(r.Name))
		statusW = max(statusW, len(statusLabel(r.Status)))
	}

	for _, r := range results {
		detail := r.Detail
		if r.Status == doctor.StatusProblem && len(r.Scope) > 0 {
			detail += " (affects only " + strings.Join(r.Scope, ", ") + ")"
		}

		label := fmt.Sprintf("%-*s", statusW, statusLabel(r.Status))
		fmt.Fprintf(w, "%-*s  %s  %s\n", nameW, r.Name, checkStyle(r.Status).Render(label), detail)

		if r.Remedy != "" {
			fmt.Fprintf(w, "%-*s  %s\n", nameW+2+statusW, "", faint.Render("→ "+r.Remedy))
		}
	}
}

func statusLabel(status string) string {
	if status == doctor.StatusNotNeeded {
		return "not needed here"
	}

	return status
}

func checkStyle(status string) lipgloss.Style {
	switch status {
	case doctor.StatusOK:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	case doctor.StatusProblem:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	default:
		return lipgloss.NewStyle().Faint(true)
	}
}
