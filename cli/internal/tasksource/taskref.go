package tasksource

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// driverTitle strips the prefix tracker.title wrote, as the host's titles
// come stripped.
func driverTitleFor(t *target.Target, r ref.Ref, doc string) string {
	title := hostops.TaskTitle(strings.TrimLeft(doc, " \t\r\n"))

	return tasktitle.Clean(t.Effective.TitleTemplate.Strip(title, r.ID, r.Number), r.ID)
}
