package launch

// A Dependency is what --after records on the new branch: the base task,
// its branch as a bare name (`origin/` stripped, since the pr step targets
// a branch and ship looks it up local-first), and where its plan is.
type Dependency struct {
	Task    string
	Branch  string
	Plan    string // the plan's path, or "" when no checkout can reach it
	Planned bool   // the plan exists at that path
}
