// Package branchmetatest answers branch records from memory, so a test sets
// and checks a record by branch and key instead of scripting git's argv.
package branchmetatest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
)

// A Write is one change a Memory took through branchmeta.
type Write struct {
	Branch string
	Key    branchmeta.Key
	Value  string
	Unset  bool
}

// Memory is a branchmeta.Backend. It ignores the directory, as every
// worktree of a repository shares one local config.
type Memory struct {
	mu      sync.Mutex
	entries []branchmeta.Entry
	writes  []Write
	err     error
	// scripted keys answer reads from a queue, the last answer repeating,
	// whatever the code writes, until a Seed
	scripted map[string][]string
	// failReads and failWrites fail a read or a write of a key, by its full name
	failReads, failWrites map[string]error
	// OnWrite, when set, sees every write as it is taken, so a test can
	// order writes against its other fakes' calls
	OnWrite func(Write)
}

// Use installs a fresh Memory for the test and puts the previous backend
// back on cleanup.
func Use(t testing.TB) *Memory {
	t.Helper()

	m := &Memory{}
	t.Cleanup(branchmeta.SetBackend(m))

	return m
}

// Seed sets a key as something outside the code would, without recording a
// write, and ends any script on it.
func (m *Memory) Seed(branch string, key branchmeta.Key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.scripted, key.For(branch))
	m.put(branch, key, value)
}

// Clear drops every record, as if no branch had any, keeping the writes taken.
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries, m.scripted = nil, nil
}

// Script makes Get of the key answer values in order, the last repeating, for
// a key something outside the code changes between reads. All, and so Load and
// LoadAll, still answer the stored value. The code's own writes do not change
// the answers; a Seed ends the script.
func (m *Memory) Script(branch string, key branchmeta.Key, values ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.scripted == nil {
		m.scripted = map[string][]string{}
	}

	m.scripted[key.For(branch)] = values
}

// FailRead makes every Get of the key on branch fail with err. All, and so
// Load and LoadAll, still succeed: use Fail to break those.
func (m *Memory) FailRead(branch string, key branchmeta.Key, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failReads == nil {
		m.failReads = map[string]error{}
	}

	m.failReads[key.For(branch)] = err
}

// FailWrite makes every write to the key on branch fail with err.
func (m *Memory) FailWrite(branch string, key branchmeta.Key, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failWrites == nil {
		m.failWrites = map[string]error{}
	}

	m.failWrites[key.For(branch)] = err
}

// Fail makes every call fail with err until Fail(nil).
func (m *Memory) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.err = err
}

// Value is the key's value on branch, and whether it is set.
func (m *Memory) Value(branch string, key branchmeta.Key) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := m.find(branch, key)
	if i < 0 {
		return "", false
	}

	return m.entries[i].Value, true
}

// Writes is every change taken, in order.
func (m *Memory) Writes() []Write {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.writes)
}

// Wrote reports whether the key was set to value on branch.
func (m *Memory) Wrote(branch string, key branchmeta.Key, value string) bool {
	return slices.Contains(m.Writes(), Write{Branch: branch, Key: key, Value: value})
}

// Unsetted reports whether the key was unset on branch.
func (m *Memory) Unsetted(branch string, key branchmeta.Key) bool {
	return slices.Contains(m.Writes(), Write{Branch: branch, Key: key, Unset: true})
}

// Touched reports whether any write named the key on branch.
func (m *Memory) Touched(branch string, key branchmeta.Key) bool {
	return slices.ContainsFunc(m.Writes(), func(w Write) bool { return w.Branch == branch && w.Key == key })
}

// String is the write as a test's call log shows it.
func (w Write) String() string {
	if w.Unset {
		return "unset " + w.Key.For(w.Branch)
	}

	return "set " + w.Key.For(w.Branch) + " " + w.Value
}

func (m *Memory) Get(_ context.Context, _, branch string, key branchmeta.Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return "", m.err
	}

	if err := m.failReads[key.For(branch)]; err != nil {
		return "", err
	}

	if queue, ok := m.scripted[key.For(branch)]; ok && len(queue) > 0 {
		if len(queue) > 1 {
			m.scripted[key.For(branch)] = queue[1:]
		}

		return queue[0], nil
	}

	if i := m.find(branch, key); i >= 0 {
		return m.entries[i].Value, nil
	}

	return "", nil
}

func (m *Memory) Set(_ context.Context, _, branch string, key branchmeta.Key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	if err := m.failWrites[key.For(branch)]; err != nil {
		return err
	}

	m.put(branch, key, value)
	m.wrote(Write{Branch: branch, Key: key, Value: value})

	return nil
}

func (m *Memory) Unset(_ context.Context, _, branch string, key branchmeta.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	if err := m.failWrites[key.For(branch)]; err != nil {
		return err
	}

	_, wasScripted := m.scripted[key.For(branch)]

	i := m.find(branch, key)
	if i < 0 && !wasScripted {
		return fmt.Errorf("git config: %s is not set", key.For(branch))
	}

	if i >= 0 {
		m.entries = slices.Delete(m.entries, i, i+1)
	}
	m.wrote(Write{Branch: branch, Key: key, Unset: true})

	return nil
}

func (m *Memory) All(context.Context, string) ([]branchmeta.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}

	return slices.Clone(m.entries), nil
}

func (m *Memory) wrote(w Write) {
	m.writes = append(m.writes, w)
	if m.OnWrite != nil {
		m.OnWrite(w)
	}
}

func (m *Memory) find(branch string, key branchmeta.Key) int {
	return slices.IndexFunc(m.entries, func(e branchmeta.Entry) bool { return e.Branch == branch && e.Key == key })
}

func (m *Memory) put(branch string, key branchmeta.Key, value string) {
	if i := m.find(branch, key); i >= 0 {
		m.entries[i].Value = value

		return
	}

	m.entries = append(m.entries, branchmeta.Entry{Branch: branch, Key: key, Value: value})
}
