// Package mcp is the f10 mcp command: the daemon's API as MCP tools over
// stdio, for Claude Desktop and local agents.
package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/buildinfo"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/mcp"
	"github.com/amberpixels/f10/cli/internal/remote"
)

// socketURL is the base calls on the socket go to; the transport dials the
// socket whatever host it names.
const socketURL = "http://f10-daemon"

// defaultScopes is what a bare f10 mcp offers: reading and answering asks,
// so a client configured once cannot start or finish work by accident.
var defaultScopes = []remote.Scope{remote.ScopeRead, remote.ScopeAnswer}

func Command() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "serve the daemon's API as MCP tools over stdio, for Claude Desktop and other MCP clients",
		Description: "Each tool is one API method, generated from the proto schema. The scopes decide which " +
			"tools are offered (" + remote.ScopeList() + "); with neither flag, read and answer.",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "scope",
				Usage: "the tools to offer, by scope, repeated or comma-separated: " + remote.ScopeList(),
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "offer the tools a device token's scopes allow, by the token's name (f10 remote token list)",
			},
		},
		Action: run,
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	scopes, check, err := resolve(cmd.StringSlice("scope"), cmd.String("token"))
	if err != nil {
		return err
	}

	dir := daemon.RunDir()
	ensurer := &daemon.Ensurer{Dir: dir, Version: buildinfo.Version()}

	// a daemon on another API version fails here, before the client connects;
	// each call ensures again, because the daemon exits when idle while the
	// client stays connected
	if _, err := ensurer.Ensure(ctx); err != nil {
		return err
	}

	server := mcp.New(mcp.Options{
		Allow:   remote.Allows(scopes),
		HTTP:    daemon.SocketHTTP(dir),
		BaseURL: socketURL,
		Before: func(ctx context.Context) error {
			if err := check(); err != nil {
				return err
			}

			_, err := ensurer.Ensure(ctx)

			return err
		},
		Version: buildinfo.Version(),
	})

	return server.Run(ctx, &sdk.StdioTransport{})
}

// resolve is the scopes the flags name, and a check run before each call:
// a token's check fails once it is revoked. A token is named, never given
// as its secret: the socket checks no token, so the secret would grant
// nothing here and would only sit in argv and in the client's config.
func resolve(scopeFlags []string, token string) ([]remote.Scope, func() error, error) {
	none := func() error { return nil }

	switch {
	case token != "" && len(scopeFlags) > 0:
		return nil, nil, errors.New("give --scope or --token, not both")
	case len(scopeFlags) > 0:
		scopes, err := remote.ParseScopes(scopeFlags)

		return scopes, none, err
	case token == "":
		return defaultScopes, none, nil
	}

	dir, err := remote.Dir()
	if err != nil {
		return nil, nil, err
	}

	store := remote.NewStore(dir)

	tok, err := named(store, token)
	if err != nil {
		return nil, nil, err
	}

	check := func() error {
		if _, err := named(store, token); err != nil {
			return fmt.Errorf("the device token %q was revoked", tok.Name)
		}

		return nil
	}

	return tok.Scopes, check, nil
}

// named is the token called name in store.
func named(store *remote.Store, name string) (remote.Token, error) {
	list, err := store.List()
	if err != nil {
		return remote.Token{}, err
	}

	for _, t := range list {
		if t.Name == name {
			return t, nil
		}
	}

	return remote.Token{}, fmt.Errorf("no device token is named %q: f10 remote token list", name)
}
