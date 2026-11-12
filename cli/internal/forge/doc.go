// Package forge binds a forge's CLI, gh or glab. Every call goes through
// Host, so each CLI's spelling is picked in one place and its JSON read into
// one shape per noun; a non-zero exit is the CLI's own stderr verbatim,
// since it names the cause better than any paraphrase.
package forge
