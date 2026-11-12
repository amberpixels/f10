package testkit

import (
	"github.com/amberpixels/f10/cli/internal/forge"
)

const (
	LsHeads   = "git ls-remote --heads origin"
	OriginURL = "git config --get remote.origin.url"
	RemoteURL = "git remote get-url origin"
	GhLogin   = "gh api user"
)

func GhHeadOf(branch string) string {
	return "gh pr list --head " + branch + " --state all --json " + forge.HeadFields + " --limit 100"
}

func GhStackOf(branch string) string {
	return "gh pr list --base " + branch + " --state open --limit 1000 --json number,url,headRefName"
}
