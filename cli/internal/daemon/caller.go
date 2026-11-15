package daemon

import "context"

// A Caller is who makes a call on the tailnet: the device token's name, its
// scopes and the procedures they allow. A call with none in its context
// came over the unix socket, where every procedure is allowed.
type Caller struct {
	Token      string
	Scopes     []string
	Procedures []string
}

type callerKey struct{}

// WithCaller marks ctx as a call by c, which the tailnet's token check does
// so Hello can say what the caller may call without this package knowing
// tokens.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerOf is the caller WithCaller set, and false for a local call.
func CallerOf(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)

	return c, ok
}
