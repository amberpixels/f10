// Package remote is the f10 remote command: the tailnet listener's setting,
// the device tokens remote calls carry, and the service that keeps the
// daemon up.
package remote

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/remote"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:  "remote",
		Usage: "serve the daemon's API to your other devices on the tailnet: the listener, device tokens and the always-on service",
		Description: "The listener binds only this machine's Tailscale address, and every call on it carries a device " +
			"token whose scopes (" + remote.ScopeList() + ") decide which methods it may call. " +
			"The unix socket keeps full access for the local user.",
		Commands: []*cli.Command{
			{
				Name:  "on",
				Usage: "turn the tailnet listener on, and start the daemon",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "port",
						Usage: fmt.Sprintf("the port to listen on (default %d)", remote.DefaultPort),
					},
				},
				Action: runOn,
			},
			{Name: "off", Usage: "turn the tailnet listener off", Action: runOff},
			{
				Name:  "token",
				Usage: "the device tokens remote calls carry",
				Commands: []*cli.Command{
					{
						Name:      "add",
						Usage:     "issue a token for a device and print it once",
						ArgsUsage: "<name>",
						Flags: []cli.Flag{
							&cli.StringSliceFlag{
								Name:     "scope",
								Usage:    "what the token may do, repeated or comma-separated: " + remote.ScopeList(),
								Required: true,
							},
						},
						Action: runTokenAdd,
					},
					{Name: "list", Usage: "the tokens, by name, with their scopes", Action: runTokenList},
					{Name: "revoke", Usage: "delete a token", ArgsUsage: "<name>", Action: runTokenRevoke},
				},
			},
			{
				Name:   "install",
				Usage:  "install and load the service that starts the daemon at login and restarts it (launchd, systemd)",
				Action: runInstall,
			},
			{Name: "uninstall", Usage: "unload and remove the service", Action: runUninstall},
		},
	}
}

func runOn(ctx context.Context, cmd *cli.Command) error {
	dir, err := remote.Dir()
	if err != nil {
		return err
	}

	c, err := remote.ReadConfig(dir)
	if err != nil {
		return err
	}

	c.Enabled = true
	if p := cmd.Int("port"); p > 0 {
		c.Port = p
	}

	if err := remote.WriteConfig(dir, c); err != nil {
		return err
	}

	run := daemon.RunDir()
	if _, err := (&daemon.Ensurer{Dir: run, Version: buildinfo.Version()}).Ensure(ctx); err != nil {
		return fmt.Errorf("the listener is on, but the daemon did not start: %w", err)
	}

	writeListener(ctx, cmd.Writer, run, c)

	if s := service(); s.Supported() {
		if _, installed := s.InstalledProgram(); !installed {
			fmt.Fprintln(cmd.Writer,
				"the daemon stops at logout and does not start after a reboot: f10 remote install keeps it up")
		}
	}

	return nil
}

func runOff(ctx context.Context, cmd *cli.Command) error {
	dir, err := remote.Dir()
	if err != nil {
		return err
	}

	c, err := remote.ReadConfig(dir)
	if err != nil {
		return err
	}

	c.Enabled = false
	if err := remote.WriteConfig(dir, c); err != nil {
		return err
	}

	// a running daemon closes the listener as it answers
	st, err := daemon.GetStatus(ctx, daemon.RunDir(), buildinfo.Version())

	switch {
	case errors.Is(err, daemon.ErrNotRunning):
		fmt.Fprintln(cmd.Writer, "tailnet listener off")
	case err != nil:
		fmt.Fprintf(cmd.Writer,
			"tailnet listener off; the daemon did not answer, and closes it within a minute: %v\n", err)
	case st.Remote.Addr != "":
		return fmt.Errorf("the setting is off, but the daemon still listens on %s: f10 daemon logs", st.Remote.Addr)
	default:
		fmt.Fprintln(cmd.Writer, "tailnet listener off")
	}

	return nil
}

// writeListener says where the daemon listens now, or why it does not.
func writeListener(ctx context.Context, w io.Writer, run string, c remote.Config) {
	st, err := daemon.GetStatus(ctx, run, buildinfo.Version())

	switch {
	case err != nil:
		fmt.Fprintf(w, "tailnet listener on, port %d; the daemon did not report it: %v\n", c.ListenPort(), err)
	case st.Remote.Addr != "":
		fmt.Fprintf(w, "tailnet listener on %s\n", st.Remote.Addr)
		fmt.Fprintf(w, "web client at %s\n", cmp.Or(st.Remote.URL, "http://"+st.Remote.Addr+"/"))

		if st.Remote.HTTPSReason != "" {
			fmt.Fprintf(w, "no HTTPS, so the page does not install or notify: %s\n", st.Remote.HTTPSReason)
		}
	default:
		fmt.Fprintf(w, "tailnet listener on, port %d, not listening yet: %s\n", c.ListenPort(), st.Remote.Reason)
		fmt.Fprintln(w, "the daemon binds it within a minute of the cause clearing")
	}
}

func runTokenAdd(_ context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return errors.New("usage: f10 remote token add <name> --scope <scopes>")
	}

	scopes, err := remote.ParseScopes(cmd.StringSlice("scope"))
	if err != nil {
		return err
	}

	store, err := tokens()
	if err != nil {
		return err
	}

	secret, err := store.Add(name, scopes, time.Now())
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.Writer, secret)
	fmt.Fprintf(cmd.ErrWriter, "token %s (%s): shown only this once; f10 keeps only its hash\n",
		name, joinScopes(scopes))

	return nil
}

func runTokenList(_ context.Context, cmd *cli.Command) error {
	store, err := tokens()
	if err != nil {
		return err
	}

	list, err := store.List()
	if err != nil {
		return err
	}

	if len(list) == 0 {
		fmt.Fprintln(cmd.Writer, "no device tokens: f10 remote token add <name> --scope ...")

		return nil
	}

	width := 0
	for _, t := range list {
		width = max(width, len(t.Name))
	}

	for _, t := range list {
		fmt.Fprintf(cmd.Writer, "%-*s  %s  %s\n",
			width, t.Name, t.Created.Local().Format(time.DateOnly), joinScopes(t.Scopes))
	}

	return nil
}

func runTokenRevoke(_ context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return errors.New("usage: f10 remote token revoke <name>")
	}

	store, err := tokens()
	if err != nil {
		return err
	}

	ok, err := store.Revoke(name)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("no token named %s", name)
	}

	fmt.Fprintf(cmd.Writer, "token %s revoked\n", name)

	return nil
}

func runInstall(ctx context.Context, cmd *cli.Command) error {
	s := service()
	if err := s.Install(ctx); err != nil {
		return err
	}

	fmt.Fprintf(cmd.Writer, "service installed: %s\n", s.Describe())

	dir, err := remote.Dir()
	if err != nil {
		return err
	}

	if c, err := remote.ReadConfig(dir); err == nil && !c.Enabled {
		fmt.Fprintln(cmd.Writer, "the tailnet listener is off: f10 remote on")
	}

	return nil
}

func runUninstall(ctx context.Context, cmd *cli.Command) error {
	s := service()

	ok, err := s.Uninstall(ctx)
	if err != nil {
		return err
	}

	if !ok {
		fmt.Fprintln(cmd.Writer, "no service installed")

		return nil
	}

	fmt.Fprintln(cmd.Writer,
		"service removed: the daemon stopped each drive at its next tick; f10 drive --resume <n> goes on with one")

	return nil
}

func tokens() (*remote.Store, error) {
	dir, err := remote.Dir()
	if err != nil {
		return nil, err
	}

	return remote.NewStore(dir), nil
}

// service is the daemon's service for this machine, carrying this shell's
// environment and the f10 its PATH finds.
func service() *remote.Service {
	home, _ := os.UserHomeDir()

	var env []string

	for _, k := range remote.ServiceEnv {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			env = append(env, k+"="+v)
		}
	}

	return &remote.Service{
		GOOS:    runtime.GOOS,
		Home:    home,
		UID:     os.Getuid(),
		Program: program(),
		Env:     env,
		Log:     daemon.LogPath(daemon.RunDir()),
		Run:     runQuiet,
		Drain:   drainForRestart,
	}
}

// program is the f10 on PATH, the plugin's shim in a session, else this
// binary. A shim under a versioned plugin directory stays on that release
// after an upgrade, which doctor flags.
func program() string {
	if p, err := exec.LookPath("f10"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}

	exe, _ := os.Executable()

	return exe
}

// drainForRestart stops the running daemon as an upgrade does, so the
// daemon the service restarts resumes its drives.
func drainForRestart(ctx context.Context) error {
	err := daemon.Stop(ctx, daemon.RunDir(), true, true)
	if errors.Is(err, daemon.ErrNotRunning) {
		return nil
	}

	return err
}

func runQuiet(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return nil
}

func joinScopes(scopes []remote.Scope) string {
	names := make([]string, len(scopes))
	for i, s := range scopes {
		names[i] = string(s)
	}

	return strings.Join(names, ",")
}
