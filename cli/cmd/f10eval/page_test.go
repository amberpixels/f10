package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The TS tests in ts/web/src/report read the same golden payload, so one
// canned run folder pins both sides of the page.
var update = flag.Bool("update", false, "rewrite testdata/runfolder/payload.json")

const cannedReport = "testdata/runfolder/feature-small.opus-high.json"

func TestRunPayloadGolden(t *testing.T) {
	t.Parallel()

	p, _, err := runPayloadFor(cannedReport)
	if err != nil {
		t.Fatal(err)
	}

	got, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')
	golden := filepath.Join("testdata", "runfolder", "payload.json")

	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("payload differs from %s; rerun with -update and check the TS tests", golden)
	}

	if len(p.Times) != len(p.Transcript) || len(p.CostUSD) != len(p.Transcript) {
		t.Errorf("times and costs do not align with the transcript: %d, %d, %d",
			len(p.Times), len(p.CostUSD), len(p.Transcript))
	}

	// the first turn's exact cost stands once its result line is read
	if c := p.CostUSD[5]; c != 0.5 || p.Estimated[5] {
		t.Errorf("cost at the first result = %v (estimated %v), want 0.5 exact", c, p.Estimated[5])
	}

	if logo := p.Before["logo.png"]; logo.Skip != "binary" || logo.Text != "" || logo.Size == 0 {
		t.Errorf("logo.png = %+v, want a binary file with its size and no text", logo)
	}
}

func TestPageTreeSkipsLargeFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("a"), maxPageText+1), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("big.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	files, err := readPageTree(dir)
	if err != nil {
		t.Fatal(err)
	}

	if f := files["big.txt"]; f.Skip != "large" || f.Text != "" {
		t.Errorf("big.txt = skip %q, %d bytes of text; want large with none", f.Skip, len(f.Text))
	}

	if f := files["link"]; f.Skip != "link" {
		t.Errorf("link = skip %q, want link", f.Skip)
	}

	// files under the per-file limit still stop at the tree's budget
	many := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q"} {
		if err := os.WriteFile(filepath.Join(many, name), bytes.Repeat([]byte("a"), maxPageText), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tree, err := readPageTree(many)
	if err != nil {
		t.Fatal(err)
	}

	if tree["p"].Skip != "" || tree["q"].Skip != "large" {
		t.Errorf("p skip %q, q skip %q; want p's text kept and q past the budget", tree["p"].Skip, tree["q"].Skip)
	}

	if got, err := readPageTree(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Errorf("a missing tree = %v, %v; want empty", got, err)
	}
}

var external = regexp.MustCompile(`<(?:script|link|img|iframe)[^>]*\s(?:src|href)=`)

// The page opens from file:// with the network off: nothing it loads leaves
// the file, and a transcript holding a closing tag stays inside its tag.
func TestWritePageSelfContained(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := &runPayload{
		Report:     Report{Scenario: "s"},
		Transcript: []string{`{"result":"</script><script>alert(1)</script>"}`},
	}

	if err := writePage(dir, "s <x>", pagePayload{Kind: "run", Run: p}); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, pageFile))
	if err != nil {
		t.Fatal(err)
	}

	page := string(body)

	if m := external.FindString(page); m != "" {
		t.Errorf("the page loads an external file: %s", m)
	}

	if strings.Contains(page, "alert(1)</script>") {
		t.Error("a transcript's </script> closed the payload's tag")
	}

	if n := strings.Count(page, "</script>"); n != 2 {
		t.Errorf("the page holds %d closing script tags, want 2: the payload's and the bundle's", n)
	}

	if !strings.Contains(page, "<title>s &lt;x&gt;</title>") {
		t.Error("the title is not escaped")
	}
}

// html rebuilds the page of every report with a run folder, then an index
// listing every report, one without a folder unlinked.
func TestHTMLRebuildsSet(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "set")
	if err := copyTree(filepath.Join("testdata", "runfolder"), dir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := htmlMain(&out, "/", []string{dir}); err != nil {
		t.Fatal(err)
	}

	runPage := filepath.Join(dir, "feature-small.opus-high", pageFile)
	if _, err := os.Stat(runPage); err != nil {
		t.Fatalf("no run page: %v", err)
	}

	if want := runPage + "\n" + filepath.Join(dir, pageFile) + "\n"; out.String() != want {
		t.Errorf("html printed %q, want %q", out.String(), want)
	}

	set := readInjected(t, filepath.Join(dir, pageFile)).Set
	if set == nil || len(set.Runs) != 2 {
		t.Fatalf("index lists %+v, want two runs", set)
	}

	pages := map[string]string{}
	for _, r := range set.Runs {
		pages[r.Report.Scenario] = r.Page
	}

	if pages["feature-small"] != "feature-small.opus-high/index.html" || pages["plan-history"] != "" {
		t.Errorf("index pages = %v, want only feature-small linked", pages)
	}
}

func readInjected(t *testing.T, path string) pagePayload {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, rest, _ := strings.Cut(string(body), `<script type="application/json" id="f10-page">`)
	data, _, _ := strings.Cut(rest, "</script>")

	var p pagePayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatal(err)
	}

	return p
}
