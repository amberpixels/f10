package taskwrite

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
)

// Document is the task token names as f10 task read prints it: a bundle's
// members joined, and with images set the linked images saved locally, so
// an agent reads files in place of urls that expire. An empty token is the
// checkout's task.
func Document(ctx context.Context, t *target.Target, token string, withImages bool) (string, error) {
	r, err := t.Reference(ctx, token)
	if err != nil {
		return "", fetchFallback(err)
	}

	members := []ref.Ref{r}
	if strings.Contains(token, ref.Separator) {
		members = r.MemberRefs()
	}

	docs := make([]string, 0, len(members))

	for _, m := range members {
		doc, err := Read(ctx, t, m)
		if err != nil {
			return "", fetchFallback(err)
		}

		if withImages {
			doc = WithImages(ctx, doc, imagesDirFor(ctx, t, m.ID), images.Downloader{Authorize: GithubAuth(ctx, t)})
		}

		docs = append(docs, strings.TrimRight(doc, "\n"))
	}

	return strings.Join(docs, "\n\n---\n\n"), nil
}

// fetchFallback tells an agent reading a failed read that the project's
// fetch adapter is its fallback: here, when it applies, not in every prompt.
func fetchFallback(err error) error {
	return fmt.Errorf("%w\nfetch the task through the project's fetch adapter instead "+
		"(project.md, Tracker)", err)
}

// WithImages ends doc with a footer naming its downloaded images. A dir
// that cannot be written fails every image, never the read.
func WithImages(ctx context.Context, doc, dir string, d images.Downloader) string {
	links := images.Links(doc)
	if len(links) == 0 {
		return doc
	}

	results, err := d.Download(ctx, dir, links)
	if err != nil {
		results = make([]images.Result, len(links))
		for i, u := range links {
			results[i] = images.Result{URL: u, Err: err}
		}
	}

	return strings.TrimRight(doc, "\n") + "\n\n" + images.Footer(results)
}

// imagesDirFor puts .f10/ into info/exclude first under stealth, since a
// read may be the first thing to create it.
func imagesDirFor(ctx context.Context, t *target.Target, id string) string {
	if !t.OutOfTree() && t.Effective.Visibility == "stealth" {
		_, _ = archive.ExcludeF10(ctx, t.Layout.CheckoutRoot)
	}

	return t.Layout.StorageRoot.Images(strings.ToUpper(id))
}

// GithubAuth sends the gh token only to GitHub's own hosts over https,
// where a private repo's attachments sit. It is read once; a failed read
// leaves the request bare, so its error names the image.
func GithubAuth(ctx context.Context, t *target.Target) func(*http.Request) {
	h, err := hostops.HostOf(t)
	if err != nil {
		return nil
	}

	var (
		once  sync.Once
		token string
	)

	return func(r *http.Request) {
		host := r.URL.Hostname()
		if r.URL.Scheme != "https" || (host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
			return
		}

		once.Do(func() {
			if tok, err := h.Token(ctx); err == nil {
				token = tok
			}
		})

		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
}
