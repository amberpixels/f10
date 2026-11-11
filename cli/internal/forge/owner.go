package forge

import (
	"context"
	"strings"
)

// An Owner says whether a request is the signed-in user's.
type Owner int

const (
	// OwnerUnknown is a request with no author, or a login the host would not tell
	OwnerUnknown Owner = iota
	OwnerMine
	OwnerOther
)

// OwnerOf compares a request's author with the signed-in login. Either one
// empty is unknown, never mine or someone else's, so each caller decides what
// an unknown owner allows.
func OwnerOf(author, login string) Owner {
	switch {
	case author == "" || login == "":
		return OwnerUnknown
	case strings.EqualFold(author, login):
		return OwnerMine
	default:
		return OwnerOther
	}
}

// Owner reads the signed-in login and compares it with p's author; a request
// with no author is unknown without asking the host.
func (h Host) Owner(ctx context.Context, p PR) (Owner, error) {
	if p.Author == "" {
		return OwnerUnknown, nil
	}

	login, err := h.Login(ctx)
	if err != nil {
		return OwnerUnknown, err
	}

	return OwnerOf(p.Author, login), nil
}
