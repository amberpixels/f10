package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The page bundle is built from ts/web/src/report by `just web::build` and
// checked in, so a Go build needs no Node.
var (
	//go:embed page/page.js
	pageJS string
	//go:embed page/page.css
	pageCSS string
)

const (
	pageFile = "index.html"
	// maxPageText and maxTreeText keep a page small enough for a browser to
	// open, whether from one large file or many small ones. A file past
	// either limit is listed without its text.
	maxPageText = 512 << 10
	maxTreeText = 8 << 20
)

// A pagePayload is what a page renders, injected as JSON. A run page's is
// its report and run folder, nearly as the folder holds them, because the
// page derives the timeline and the step replay itself.
type pagePayload struct {
	Kind string      `json:"kind"` // "run" or "set"
	Run  *runPayload `json:"run,omitempty"`
	Set  *setPayload `json:"set,omitempty"`
}

type runPayload struct {
	Report   Report          `json:"report"`
	Scenario json.RawMessage `json:"scenario,omitempty"` // inputs/scenario.json as written
	Task     string          `json:"task,omitempty"`
	Project  string          `json:"project,omitempty"`
	// Transcript is transcript.jsonl line for line. Times, CostUSD and
	// Estimated align with it. The running cost is the live view's rule,
	// computed here so its price table has one home.
	Transcript []string            `json:"transcript"`
	Times      []int64             `json:"times"`
	CostUSD    []float64           `json:"cost_usd"`
	Estimated  []bool              `json:"estimated"`
	Diff       string              `json:"diff"`
	Before     map[string]pageText `json:"before"`
	After      map[string]pageText `json:"after"`
}

// A pageText is one file of a tree. Skip says why Text is empty: "binary",
// "large" or "link". An empty Skip with empty Text is an empty file.
type pageText struct {
	Text string `json:"text,omitempty"`
	Size int64  `json:"size"`
	Skip string `json:"skip,omitempty"`
}

type setPayload struct {
	Name string   `json:"name"`
	Runs []setRun `json:"runs"`
}

// A setRun is one report of the set. Page is its page relative to the
// set's directory, empty for a report with no run folder.
type setRun struct {
	File   string `json:"file"`
	Report Report `json:"report"`
	Page   string `json:"page,omitempty"`
}

// runPayloadFor reads the report at path and the run folder it names.
func runPayloadFor(path string) (*runPayload, string, error) {
	rep, err := readReport(path)
	if err != nil {
		return nil, "", err
	}

	if rep.RunDir == "" {
		return nil, "", fmt.Errorf("%s predates run folders", filepath.Base(path))
	}

	dir := filepath.Join(filepath.Dir(path), rep.RunDir)
	p := &runPayload{Report: rep}

	if body, err := os.ReadFile(filepath.Join(dir, inputsDir, "scenario.json")); err == nil && json.Valid(body) {
		p.Scenario = body
	}

	p.Task = readOptional(filepath.Join(dir, inputsDir, "task.md"))
	p.Project = readOptional(filepath.Join(dir, inputsDir, "project.md"))
	p.Diff = readOptional(filepath.Join(dir, diffFile))

	if err := p.readTranscript(dir); err != nil {
		return nil, "", err
	}

	if p.Before, err = readPageTree(filepath.Join(dir, beforeDir)); err != nil {
		return nil, "", err
	}

	if p.After, err = readPageTree(filepath.Join(dir, afterDir)); err != nil {
		return nil, "", err
	}

	return p, dir, nil
}

// readTranscript keeps every line, a bad one included, so the times stay
// aligned. A run that errored before its first turn has none.
func (p *runPayload) readTranscript(dir string) error {
	body, err := os.ReadFile(filepath.Join(dir, transcriptFile))
	if errors.Is(err, fs.ErrNotExist) || len(body) == 0 {
		p.Transcript, p.Times, p.CostUSD, p.Estimated = []string{}, []int64{}, []float64{}, []bool{}

		return nil
	}

	if err != nil {
		return err
	}

	ms := strings.Fields(readOptional(filepath.Join(dir, timesFile)))
	p.Transcript = strings.Split(strings.TrimRight(string(body), "\n"), "\n")

	var prog progress

	for i, line := range p.Transcript {
		var t int64
		if i < len(ms) {
			t, _ = strconv.ParseInt(ms[i], 10, 64)
		}

		var e event
		if json.Unmarshal([]byte(line), &e) == nil {
			prog.apply(e)
		}

		cost, est := prog.CostUSD()
		p.Times = append(p.Times, t)
		p.CostUSD = append(p.CostUSD, math.Round(cost*1e4)/1e4)
		p.Estimated = append(p.Estimated, est)
	}

	return nil
}

func readOptional(path string) string {
	body, _ := os.ReadFile(path)

	return string(body)
}

// readPageTree is a tree's files by slash path. A missing tree, as after a
// setup error, is empty. The walk stays inside root, so a link the run left
// is listed, never followed.
func readPageTree(root string) (map[string]pageText, error) {
	files := map[string]pageText{}

	r, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}

	if err != nil {
		return nil, err
	}
	defer r.Close()

	var total int64

	err = fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		f := pageText{Size: info.Size()}

		switch {
		case d.Type()&fs.ModeSymlink != 0:
			f.Skip = "link"
		case info.Size() > maxPageText || total+info.Size() > maxTreeText:
			f.Skip = "large"
		default:
			body, err := r.ReadFile(path)
			if err != nil {
				return err
			}

			if !utf8.Valid(body) || slices.Contains(body, 0) {
				f.Skip = "binary"
			} else {
				f.Text = string(body)
				total += info.Size()
			}
		}

		files[path] = f

		return nil
	})

	return files, err
}

// writePage writes the page into dir as one self-contained file: the
// stylesheet, the payload and the script inline, so it opens from file://.
func writePage(dir, title string, payload pagePayload) error {
	data, err := json.Marshal(payload) // escapes <, so no payload text closes its tag
	if err != nil {
		return err
	}

	var b strings.Builder

	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"color-scheme\" content=\"light dark\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	fmt.Fprintf(&b, "<style>%s</style>\n", strings.ReplaceAll(pageCSS, "</style", `<\/style`))
	b.WriteString("</head>\n<body>\n<div id=\"app\"></div>\n")
	fmt.Fprintf(&b, "<script type=\"application/json\" id=\"f10-page\">%s</script>\n", data)
	fmt.Fprintf(&b, "<script>%s</script>\n", strings.ReplaceAll(pageJS, "</script", `<\/script`))
	b.WriteString("</body>\n</html>\n")

	return os.WriteFile(filepath.Join(dir, pageFile), []byte(b.String()), 0o644) //nolint:gosec // a page to open
}

// writeRunPage writes the page of the report at path into its run folder.
func writeRunPage(path string) error {
	p, dir, err := runPayloadFor(path)
	if err != nil {
		return err
	}

	title := p.Report.Scenario
	if p.Report.Agent != "" {
		title += " (" + p.Report.Agent + ")"
	}

	return writePage(dir, title, pagePayload{Kind: "run", Run: p})
}

// writeSetIndex writes dir's index.html, a row per report there.
func writeSetIndex(dir string) error {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}

	set := &setPayload{Name: filepath.Base(dir), Runs: []setRun{}}

	for _, name := range names {
		rep, err := readReport(name)
		if err != nil || rep.Scenario == "" {
			continue
		}

		r := setRun{File: filepath.Base(name), Report: rep}
		if rep.RunDir != "" {
			if _, err := os.Stat(filepath.Join(dir, rep.RunDir, pageFile)); err == nil {
				r.Page = rep.RunDir + "/" + pageFile
			}
		}

		set.Runs = append(set.Runs, r)
	}

	return writePage(dir, set.Name+" eval runs", pagePayload{Kind: "set", Set: set})
}

// htmlMain rebuilds a report set's pages from its run folders, then its
// index, so a change to the page never needs a re-run.
func htmlMain(w io.Writer, root string, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: f10eval html <report-dir>")
	}

	dir := resolveDir(root, args[0])
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a report directory", args[0])
	}

	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}

	var errs []error

	for _, name := range names {
		rep, err := readReport(name)
		if err != nil || rep.Scenario == "" || rep.RunDir == "" {
			continue
		}

		if err := writeRunPage(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(name), err))

			continue
		}

		fmt.Fprintln(w, filepath.Join(dir, rep.RunDir, pageFile))
	}

	if err := writeSetIndex(dir); err != nil {
		errs = append(errs, err)
	} else {
		fmt.Fprintln(w, filepath.Join(dir, pageFile))
	}

	return errors.Join(errs...)
}
