package tasksource

import "errors"

var ErrNoParentLink = errors.New("the tracker has no parent link")

type RawChild struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
	After  string   `json:"after"`
	Body   string   `json:"body"`
	// Stated: State is the tracker's own, as Info reports it. Only the host's
	// issues set it. A driver's rows never claim it, whatever their state.
	Stated bool `json:"-"`
}
