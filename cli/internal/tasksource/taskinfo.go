package tasksource

// A TaskInfo is what the tracker says about a task beyond its branch.
type TaskInfo struct {
	Body   string
	Closed bool
	Pull   bool // the number is a pull request: GitHub numbers issues and PRs alike
}
