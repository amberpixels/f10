package history

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/storage"
)

var kindLine = regexp.MustCompile(`^Kind:\s*(\w+)`)

// headerLines bounds the read: a plan's header lines open the file.
const headerLines = 20

// PlanKind is the Kind: line of task's plan in plansDir, else of its newest
// archived plan; a plan with none is a feature, and no plan is "".
func PlanKind(plansDir, task string) string {
	for _, path := range planFiles(plansDir, strings.ToUpper(task)) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		kind := "feature"
		sc := bufio.NewScanner(f)

		for i := 0; i < headerLines && sc.Scan(); i++ {
			if m := kindLine.FindStringSubmatch(sc.Text()); m != nil {
				kind = strings.ToLower(m[1])

				break
			}
		}

		_ = f.Close()

		return kind
	}

	return ""
}

// planFiles is the live plan, then archived ones newest first.
func planFiles(plansDir, task string) []string {
	out := []string{filepath.Join(plansDir, task+".md")}

	matches, _ := filepath.Glob(filepath.Join(storage.ArchiveOf(plansDir), task+".*md"))

	n := func(path string) int {
		mid := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), task+"."), "md")
		v, _ := strconv.Atoi(strings.TrimSuffix(mid, "."))

		return v
	}

	slices.SortFunc(matches, func(a, b string) int { return n(b) - n(a) })

	return append(out, matches...)
}
