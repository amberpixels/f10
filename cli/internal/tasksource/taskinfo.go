package tasksource

// A TaskInfo is what the tracker says about a task beyond its branch.
type TaskInfo struct {
	Body   string
	Closed bool
	Stated bool     // Closed comes from the tracker: a driver reports no state
	Pull   bool     // the number is a pull request: GitHub numbers issues and PRs alike
	Labels []string // the host's labels; a driver reports none
}
