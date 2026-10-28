package facts

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/amberpixels/f10/cli/internal/frontmatter"
	"github.com/amberpixels/f10/cli/internal/mdline"
)

// renderDeclared writes a frontmatter block, fences included, or "" when
// nothing is declared.
func renderDeclared(d Declared) (string, error) {
	if isEmpty(d) {
		return "", nil
	}

	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	if err := enc.Encode(d); err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}

	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("rendering frontmatter: %w", err)
	}

	return "---\n" + b.String() + "---\n", nil
}

func isEmpty(d Declared) bool {
	return d.Visibility == "" && d.Storage == "" && d.Layering == "" && d.Tracker.empty() &&
		d.MergeMethod == "" && d.Catchup == "" && d.CatchupStacked == "" && d.Traces == "" && d.ReviewTraces == "" && d.Lore == "" && d.StaleAfter == "" && d.DriverTimeout == "" &&
		d.Flows == nil && d.FlowByArea == nil && d.DriveFinish == "" && d.DriveEnv == nil && d.Watch == "" &&
		d.Pipelines == nil && d.Planless == nil && d.ByArea == nil && d.ReportBack == "" &&
		d.DriveChain == nil && d.Review == (ReviewDecl{}) && d.Standup.Upkeep == nil
}

// A Migration is what `f10 init --migrate` makes of one project.md: the
// file with its facts in a frontmatter block on top, and the prose each
// moved fact came from, for the user to delete.
type Migration struct {
	Content    string
	Moved      []string // the frontmatter keys written
	Superseded []string // "<key>: <where in the prose>"
}

// ErrHasFrontmatter refuses a second migration.
var ErrHasFrontmatter = errors.New("already has a frontmatter block and nothing left to migrate")

// oldTracesKey is the one key a frontmatter block can still need renamed.
var oldTracesKey = regexp.MustCompile(`(?m)^review_traces:`)

// renameTraces rewrites `review_traces:` to `traces:` inside the block,
// byte for byte otherwise, since re-encoding would reorder keys and drop
// comments. Nothing to rename is ErrHasFrontmatter.
func renameTraces(data, block []byte) (*Migration, error) {
	start := bytes.Index(data, block)
	if start < 0 || !oldTracesKey.Match(block) {
		return nil, ErrHasFrontmatter
	}

	renamed := oldTracesKey.ReplaceAll(block, []byte("traces:"))
	content := append(append(append([]byte{}, data[:start]...), renamed...), data[start+len(block):]...)

	return &Migration{Content: string(content), Moved: []string{"traces"}}, nil
}

// Migrate reads the facts one project.md declares in prose, with the same
// readers the fallback uses, and puts them in a frontmatter block above the
// untouched file. Only what the file declares moves: defaults and
// detection stay out, so the block says no more than the prose did. A file
// that already has a block gets its old keys rewritten: pipelines,
// planless, pipeline_by_area and drive_chain into flows, old-form entries
// into the colon form, review_traces into traces; and every prose fact the
// block does not declare yet added to it.
func Migrate(data []byte, path string) (*Migration, error) {
	if block, body, ok := frontmatter.Split(data); ok {
		return migrateBlock(data, block, body, path)
	}

	secs, _ := parseFile(data, path, "main")
	body := func(name string) string { return secs[name].body }
	m := &Migration{}

	d, lifts := readProse(body)
	for _, l := range lifts {
		m.Moved = append(m.Moved, l.key)
		m.Superseded = append(m.Superseded, l.key+": "+l.where)
	}

	pipes, planless := prosePipelines(body("Ship pipeline(s)"))
	if chain := proseChain(body("Drive chain")); pipes != nil || len(chain) > 0 {
		flows, finish := convertLegacy(pipes, planless, chain, d.Review.Reviewer != "")

		d.Flows = flows
		d.DriveFinish = onOff(finish)

		for _, name := range []string{"Ship pipeline(s)", "Drive chain"} {
			if strings.TrimSpace(body(name)) != "" {
				m.Superseded = append(m.Superseded, "flows: the "+name+" section")
			}
		}

		m.Moved = append(m.Moved, "flows")
	}

	if bad := d.check(); len(bad) > 0 {
		return nil, fmt.Errorf("the prose does not migrate cleanly: %s", strings.Join(bad, "; "))
	}

	block, err := renderDeclared(d)
	if err != nil {
		return nil, err
	}

	m.Content = block + string(data)

	return m, nil
}

// A lift is one fact read from prose: its frontmatter key, where the prose
// says it, and the value the block takes.
type lift struct {
	key, where string
	value      any
}

// readProse reads every prose fact but flows, which need the old pipeline
// conversion and stay with the callers.
func readProse(body func(string) string) (Declared, []lift) {
	var (
		d     Declared
		lifts []lift
	)

	moved := func(key, where string, value any) {
		lifts = append(lifts, lift{key: key, where: where, value: value})
	}

	if w := proseWord(body("Visibility"), "public", "stealth"); w != "" {
		d.Visibility = w
		moved("visibility", "the Visibility section", w)
	}

	if w := proseWord(body("Storage"), "out-of-tree", "in-repo"); w != "" {
		d.Storage = w
		moved("storage", "the Storage section", w)
	}

	if decl := mdline.Bare(firstLayering(body("Layering"))); decl != "" {
		d.Layering = decl
		moved("layering", "the Layering line", decl)
	}

	tracker := body("Tracker")
	if kind := proseTrackerKind(strings.ToLower(tracker)); kind != "" {
		d.Tracker.Kind = kindKey(kind)
		moved("tracker.kind", "the tracker's name in the Tracker section", d.Tracker.Kind)
	}

	if prefix := idPrefix(tracker); prefix != "" {
		d.Tracker.ID = prefix + "-###"
		moved("tracker.id", "the task id format in the Tracker section", d.Tracker.ID)
	}

	hosting := mdline.Unmark(body("Hosting & PR"))
	if method := firstMatch(mergeMethodRe, hosting); method != "" {
		d.MergeMethod = method
		moved("merge_method", "`merge method:` in the Hosting & PR section", method)
	}

	if catchup := firstMatch(catchupRe, hosting); catchup != "" {
		d.Catchup = catchup
		moved("catchup", "`catchup:` in the Hosting & PR section", catchup)
	}

	if r := proseReviewDecl(body("Review")); r != (ReviewDecl{}) {
		d.Review = r
		moved("review", "the Reviewer, Arrives as, Trigger, Done and Settled items in the Review section", r)
	}

	return d, lifts
}

// declares says whether a block already holds a lift's key. Review moves
// whole: its items belong together.
func declares(d Declared, key string) bool {
	switch key {
	case "visibility":
		return d.Visibility != ""
	case "storage":
		return d.Storage != ""
	case "layering":
		return d.Layering != ""
	case "tracker.kind":
		return d.Tracker.Kind != ""
	case "tracker.id":
		return d.Tracker.ID != ""
	case "merge_method":
		return d.MergeMethod != ""
	case "catchup":
		return d.Catchup != ""
	default: // review
		return d.Review != (ReviewDecl{})
	}
}

// migrateBlock runs the passes a file with a block needs: old flow keys,
// else the review_traces rename, then the prose facts the block lacks.
// Nothing for any of them is ErrHasFrontmatter.
func migrateBlock(data, block, body []byte, path string) (*Migration, error) {
	m, err := migrateFlows(data, block, body, path)
	if errors.Is(err, ErrHasFrontmatter) {
		m, err = renameTraces(data, block)
	}

	switch {
	case errors.Is(err, ErrHasFrontmatter):
		m = &Migration{Content: string(data)}
	case err != nil:
		return nil, err
	}

	lifted, err := liftProse([]byte(m.Content), path)

	switch {
	case errors.Is(err, ErrHasFrontmatter):
		if len(m.Moved) == 0 {
			return nil, ErrHasFrontmatter
		}

		return m, nil
	case err != nil:
		return nil, err
	}

	lifted.Moved = append(m.Moved, lifted.Moved...)
	lifted.Superseded = append(m.Superseded, lifted.Superseded...)

	return lifted, nil
}

// liftProse adds every prose fact the block does not declare to the end of
// it, tracker keys inside its tracker mapping. A fact both declare is left
// as it is: config already warns when they agree and errors when they do
// not. The block is edited as a node, so key order and comments stay.
func liftProse(data []byte, path string) (*Migration, error) {
	block, body, _ := frontmatter.Split(data)

	d, err := decodeDeclared(block)
	if err != nil {
		return nil, fmt.Errorf("reading the frontmatter: %w", err)
	}

	secs, _ := parseFile(body, path, "main")
	_, lifts := readProse(func(name string) string { return secs[name].body })

	lifts = slices.DeleteFunc(lifts, func(l lift) bool { return declares(d, l.key) })
	if len(lifts) == 0 {
		return nil, ErrHasFrontmatter
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(block, &doc); err != nil {
		return nil, fmt.Errorf("reading the frontmatter: %w", err)
	}

	// an empty or comment-only block decodes to no mapping: start one,
	// keeping the comments
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{
			Kind:        yaml.MappingNode,
			Tag:         "!!map",
			HeadComment: strings.TrimSpace(string(block)),
		}}}
	}

	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("reading the frontmatter: not a mapping")
	}

	root := doc.Content[0]
	m := &Migration{}

	for _, l := range lifts {
		value := &yaml.Node{}
		if err := value.Encode(l.value); err != nil {
			return nil, fmt.Errorf("rendering %s: %w", l.key, err)
		}

		into, key := root, l.key
		if sub, ok := strings.CutPrefix(l.key, "tracker."); ok {
			into, key = mappingAt(root, "tracker"), sub
		}

		into.Content = append(into.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
		m.Moved = append(m.Moved, l.key)
		m.Superseded = append(m.Superseded, l.key+": "+l.where)
	}

	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("rendering frontmatter: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendering frontmatter: %w", err)
	}

	rewritten, err := decodeDeclared(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("the rewritten frontmatter does not read back: %w", err)
	}

	if bad := rewritten.check(); len(bad) > 0 {
		return nil, fmt.Errorf("the prose does not migrate cleanly: %s", strings.Join(bad, "; "))
	}

	// rebuilt around the body, since an empty block has no bytes to find
	m.Content = "---\n" + b.String() + "---\n" + string(body)

	return m, nil
}

// mappingAt is the mapping under key in root: the existing one, a new one
// in place of a null value, or a new one at the end.
func mappingAt(root *yaml.Node, key string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}

		if value := root.Content[i+1]; value.Kind == yaml.MappingNode {
			return value
		}

		root.Content[i+1] = m

		return m
	}

	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, m)

	return m
}

// firstLayering is the value of a Layering section or line.
func firstLayering(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.ToLower(line); strings.Contains(line, "replaces") {
			return "replaces"
		} else if strings.Contains(line, "extends") {
			return "extends"
		}
	}

	return ""
}

func kindKey(kind string) string {
	for key, name := range trackerKinds {
		if name == kind {
			return key
		}
	}

	return ""
}

// proseReviewDecl lifts the remote items' values out of Review prose
// as written, so the frontmatter keeps the same words.
func proseReviewDecl(body string) ReviewDecl {
	var d ReviewDecl

	for line := range strings.SplitSeq(body, "\n") {
		item, isItem := mdline.Item(line)
		if !isItem {
			continue
		}

		label, value, ok := mdline.Cut(item, " - ", ":")
		if !ok {
			continue
		}

		switch key := strings.ToLower(strings.Join(strings.Fields(label), " ")); {
		case key == "reviewer":
			d.Reviewer = value
		case strings.HasPrefix(key, "arrives"):
			d.Arrives = value
		case key == "trigger":
			d.Trigger = value
		case strings.HasPrefix(key, "done"):
			d.Done = value
		case key == "settled":
			d.Settled = value
		}
	}

	return d
}

// convertLegacy is legacyFlows over what a file declares: no pipelines is
// the old default one, no chain the old default chain.
func convertLegacy(pipes map[string][]string, planless, chain []string, remote bool) (map[string]Flow, bool) {
	if pipes == nil {
		pipes = map[string][]string{"default": {"implement", "pr"}}
	}

	if len(chain) == 0 {
		chain = strings.Fields(legacyDriveChain)
	}

	converted, finish := legacyFlows(pipes, planless, lowered(chain), remote)

	flows := map[string]Flow{}
	for name, entries := range converted {
		flows[name] = entries
	}

	return flows, finish
}

// migrateFlows rewrites the old flow keys inside an existing block, and
// review_traces with them. The block is edited as a YAML node, so key
// order and comments stay; indentation comes back as the encoder's two
// spaces. Nothing to rewrite but review_traces, or nothing at all, is
// ErrHasFrontmatter, and renameTraces keeps that case byte for byte.
func migrateFlows(data, block, body []byte, path string) (*Migration, error) {
	d, err := decodeDeclared(block)
	if err != nil {
		return nil, fmt.Errorf("reading the frontmatter: %w", err)
	}

	secs, _ := parseFile(body, path, "main")
	prosePipes, prosePlanless := prosePipelines(secs["Ship pipeline(s)"].body)
	proseChainSteps := proseChain(secs["Drive chain"].body)

	paren := false

	for _, f := range d.Flows {
		_, old := normalizeFlow(f)
		paren = paren || old
	}

	// prose sections count too: config warns about them under a block, and
	// this is the command its warning names
	prose := d.Flows == nil && (prosePipes != nil || len(proseChainSteps) > 0)
	legacy := d.Pipelines != nil || d.Planless != nil || d.ByArea != nil || d.DriveChain != nil || prose
	if !legacy && !paren {
		return nil, ErrHasFrontmatter
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(block, &doc); err != nil || len(doc.Content) == 0 ||
		doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("reading the frontmatter: not a mapping")
	}

	root := doc.Content[0]
	m := &Migration{}

	flows, finish := d.Flows, false
	converted := flows == nil

	if converted {
		pipes, planless := map[string][]string(nil), d.Planless
		if d.Pipelines != nil {
			pipes = map[string][]string{}
			for name, steps := range d.Pipelines {
				pipes[strings.ToLower(name)] = steps
			}
		} else if prosePipes != nil {
			pipes, planless = prosePipes, prosePlanless
		}

		chain := []string(d.DriveChain)
		if chain == nil {
			chain = proseChainSteps
		}

		remote := d.Review.Reviewer != "" || proseReviewDecl(secs["Review"].body).Reviewer != ""
		flows, finish = convertLegacy(pipes, planless, chain, remote)
	}

	normalized := map[string]Flow{}
	for name, f := range flows {
		entries, _ := normalizeFlow(f)
		normalized[name] = entries
	}

	flowsNode := &yaml.Node{}
	if err := flowsNode.Encode(normalized); err != nil {
		return nil, fmt.Errorf("rendering flows: %w", err)
	}

	sortFlowKeys(flowsNode)

	var (
		out   []*yaml.Node
		wrote bool
	)

	put := func(key string, value *yaml.Node, comment *yaml.Node) {
		k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		if comment != nil {
			k.HeadComment, k.LineComment = comment.HeadComment, comment.LineComment
		}

		out = append(out, k, value)
	}

	writeFlows := func(at *yaml.Node) {
		if wrote {
			return
		}

		wrote = true

		put("flows", flowsNode, at)

		// the chain said whether a drive finishes; written either way, since
		// the default is on and a chain without finish meant off
		if converted && d.DriveFinish == "" {
			put("drive_finish", &yaml.Node{Kind: yaml.ScalarNode, Value: onOff(finish)}, nil)
			m.Moved = append(m.Moved, "drive_finish")
		}
	}

	m.Moved = append(m.Moved, "flows")

	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]

		switch key.Value {
		case "flows", "pipelines":
			writeFlows(key)
		case "planless", "drive_chain":
			if d.Flows == nil && d.Pipelines == nil {
				writeFlows(key)
			}
		case "pipeline_by_area":
			if d.FlowByArea == nil {
				key.Value = "flow_by_area"
				out = append(out, key, value)
				m.Moved = append(m.Moved, "flow_by_area")
			}
		case "review_traces":
			if d.Traces == "" {
				key.Value = "traces"
				out = append(out, key, value)
				m.Moved = append(m.Moved, "traces")
			}
		default:
			out = append(out, key, value)
		}
	}

	writeFlows(nil)

	root.Content = out

	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("rendering frontmatter: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendering frontmatter: %w", err)
	}

	rewritten, err := decodeDeclared(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("the rewritten frontmatter does not read back: %w", err)
	}

	if bad := rewritten.check(); len(bad) > 0 {
		return nil, fmt.Errorf("the old keys do not migrate cleanly: %s", strings.Join(bad, "; "))
	}

	for _, name := range []string{"Ship pipeline(s)", "Drive chain"} {
		if d.Flows == nil && strings.TrimSpace(secs[name].body) != "" {
			m.Superseded = append(m.Superseded, "flows: the "+name+" section")
		}
	}

	start := bytes.Index(data, block)
	m.Content = string(data[:start]) + b.String() + string(data[start+len(block):])

	return m, nil
}

// sortFlowKeys puts default first in an encoded flows mapping, the rest by
// name, as f10 flow lists them.
func sortFlowKeys(n *yaml.Node) {
	type pair struct{ k, v *yaml.Node }

	pairs := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, pair{n.Content[i], n.Content[i+1]})
	}

	slices.SortStableFunc(pairs, func(a, b pair) int {
		switch {
		case a.k.Value == b.k.Value:
			return 0
		case a.k.Value == "default":
			return -1
		case b.k.Value == "default":
			return 1
		default:
			return strings.Compare(a.k.Value, b.k.Value)
		}
	})

	n.Content = n.Content[:0]
	for _, p := range pairs {
		n.Content = append(n.Content, p.k, p.v)
	}
}
