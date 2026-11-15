package remote

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"
)

// tokenPrefix marks an f10 device token, so one pasted elsewhere is
// recognisable.
const tokenPrefix = "f10_"

// A Token is one device's access: its name, the hash of its secret and the
// scopes it carries. The secret itself is never stored.
type Token struct {
	Name    string    `json:"name"`
	Hash    string    `json:"hash"`
	Scopes  []Scope   `json:"scopes"`
	Created time.Time `json:"created"`
}

// Has reports whether the token carries scope.
func (t Token) Has(scope Scope) bool { return slices.Contains(t.Scopes, scope) }

// A Store is the tokens file. Verify rereads it only when it changed, so a
// revoke takes effect on the next call without a restart.
type Store struct {
	Path string

	mu     sync.Mutex
	mtime  time.Time
	size   int64
	cached []Token
}

// NewStore is the tokens file in dir.
func NewStore(dir string) *Store { return &Store{Path: filepath.Join(dir, "tokens.json")} }

var tokenName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Add issues a token for name with scopes and returns its secret, which is
// shown once and never stored.
func (s *Store) Add(name string, scopes []Scope, now time.Time) (string, error) {
	if !tokenName.MatchString(name) {
		return "", fmt.Errorf("token name %q: use letters, digits, dot, dash or underscore", name)
	}

	if len(scopes) == 0 {
		return "", errors.New("a token needs at least one scope")
	}

	list, err := s.List()
	if err != nil {
		return "", err
	}

	if slices.ContainsFunc(list, func(t Token) bool { return t.Name == name }) {
		return "", fmt.Errorf("a token named %s exists: revoke it first, or pick another name", name)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	list = append(list, Token{Name: name, Hash: hash(secret), Scopes: scopes, Created: now.UTC()})

	return secret, s.write(list)
}

// List is every token, in the order they were added.
func (s *Store) List() ([]Token, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var list []Token
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s does not read as device tokens: %w", s.Path, err)
	}

	return list, nil
}

// Revoke deletes the token named name; false when none is.
func (s *Store) Revoke(name string) (bool, error) {
	list, err := s.List()
	if err != nil {
		return false, err
	}

	kept := slices.DeleteFunc(slices.Clone(list), func(t Token) bool { return t.Name == name })
	if len(kept) == len(list) {
		return false, nil
	}

	return true, s.write(kept)
}

// Verify is the token whose secret this is; false for none, and for a file
// that does not read, so a broken file refuses every call.
func (s *Store) Verify(secret string) (Token, bool) {
	list := s.load()
	sum := []byte(hash(secret))

	found, ok := Token{}, false

	// every hash is compared, so the time taken does not say which matched
	for _, t := range list {
		if subtle.ConstantTimeCompare(sum, []byte(t.Hash)) == 1 {
			found, ok = t, true
		}
	}

	return found, ok
}

func (s *Store) load() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()

	fi, err := os.Stat(s.Path)
	if err != nil {
		s.cached, s.mtime, s.size = nil, time.Time{}, 0

		return nil
	}

	if fi.ModTime().Equal(s.mtime) && fi.Size() == s.size {
		return s.cached
	}

	list, err := s.List()
	if err != nil {
		list = nil
	}

	s.cached, s.mtime, s.size = list, fi.ModTime(), fi.Size()

	return list
}

func (s *Store) write(list []Token) error {
	if list == nil {
		list = []Token{}
	}

	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return writeFile(s.Path, append(raw, '\n'))
}

func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))

	return hex.EncodeToString(sum[:])
}
