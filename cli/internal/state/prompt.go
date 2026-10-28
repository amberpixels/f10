package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const promptExt = ".prompted"

// PromptedPath is the mark a session's last prompt leaves beside its state
// file: which checkout it was typed in and when. A drive reads it to tell
// a turn that ran between two looks from one never taken.
func PromptedPath(dir, session string) string {
	return filepath.Join(dir, unsafe.ReplaceAllString(session, "_")+promptExt)
}

// A Prompt is one session's mark: the checkout root and the prompt's time.
type Prompt struct {
	Session string
	Root    string
	At      time.Time
}

// MarkPrompt writes session's mark. It never touches the run file, so a
// session f10 does not run leaves no run behind.
func MarkPrompt(dir, session, root string, at time.Time) error {
	if dir == "" || session == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	path := PromptedPath(dir, session)
	tmp := path + "." + strconv.Itoa(os.Getpid())
	body := "root " + oneLine(root) + "\nat " + strconv.FormatInt(at.UnixMilli(), 10) + "\n"

	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return fmt.Errorf("state: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("state: %w", err)
	}

	return nil
}

// LastPrompt is the newest mark any session left in root, false for none.
func LastPrompt(dir, root string) (Prompt, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Prompt{}, false
	}

	var last Prompt

	found := false

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), promptExt) {
			continue
		}

		p, ok := readPrompt(filepath.Join(dir, e.Name()))
		if ok && p.Root == root && (!found || p.At.After(last.At)) {
			last, found = p, true
		}
	}

	return last, found
}

func readPrompt(path string) (Prompt, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Prompt{}, false
	}

	p := Prompt{Session: strings.TrimSuffix(filepath.Base(path), promptExt)}

	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, _ := strings.Cut(line, " ")

		switch key {
		case "root":
			p.Root = value
		case "at":
			if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
				p.At = time.UnixMilli(ms)
			}
		}
	}

	return p, p.Root != "" && !p.At.IsZero()
}

// FileRoot is the checkout a per-session file in dir is about: a run file's
// root, a prompt mark's, or for an open wait its session's; "" for any other
// file, or a session with neither.
func FileRoot(dir, name string) string {
	path := filepath.Join(dir, name)

	switch {
	case strings.HasSuffix(name, promptExt):
		p, _ := readPrompt(path)

		return p.Root
	case strings.HasSuffix(name, fileExt):
		if r, err := Load(path); err == nil {
			return r.Root
		}
	case strings.HasSuffix(name, ".wait"):
		session := strings.TrimSuffix(name, ".wait")
		if p, ok := readPrompt(filepath.Join(dir, session+promptExt)); ok {
			return p.Root
		}

		if r, err := Load(filepath.Join(dir, session+fileExt)); err == nil {
			return r.Root
		}
	}

	return ""
}
