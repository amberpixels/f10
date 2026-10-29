package layout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The load prints the binary's verify line, and the old probe lines when the
// binary is missing or too old to know `config verify`. PATH drops the
// machine's f10, so the installed binary never answers.
func TestLoadPrintsVerify(t *testing.T) {
	script := scriptPath(t)

	cases := []struct {
		name string
		stub string // the f10 on PATH, "" for none
		want []string
		not  []string
	}{
		{
			name: "resolved by the binary",
			stub: "#!/bin/sh\n[ \"$1 $2\" = \"config verify\" ] && echo 'verify: detected (justfile)  just lint / just test'\n",
			want: []string{"verify: detected (justfile)  just lint / just test"},
			not:  []string{"note: f10 config verify", "justfile present"},
		},
		{
			name: "an old binary prints its whole config",
			stub: "#!/bin/sh\necho 'checkout root   /x'\necho 'Verify        absent    -'\n",
			want: []string{
				"note: f10 config verify gave no verify line",
				"verify: justfile present (try: just lint / just test)",
			},
			not: []string{"checkout root   /x"},
		},
		{
			name: "no binary",
			want: []string{
				"note: f10 config verify gave no verify line",
				"verify: justfile present (try: just lint / just test)",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := mkRepo(t)
			writeFile(t, filepath.Join(repo, "justfile"), "lint:\n    true\n")

			bin := t.TempDir()
			if tc.stub != "" {
				writeFile(t, filepath.Join(bin, "f10"), tc.stub)

				if err := os.Chmod(filepath.Join(bin, "f10"), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.Command("bash", script)
			cmd.Dir = repo
			cmd.Env = append(withoutPathAndHome(os.Environ()), "HOME="+t.TempDir())
			cmd.Env = append(cmd.Env, "PATH="+bin+string(os.PathListSeparator)+pathWithoutF10())

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("f10-load-project.sh failed: %v\n%s", err, out)
			}

			for _, w := range tc.want {
				if !strings.Contains(string(out), w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}

			for _, n := range tc.not {
				if strings.Contains(string(out), n) {
					t.Errorf("unexpected %q in:\n%s", n, out)
				}
			}
		})
	}
}

func withoutPathAndHome(env []string) []string {
	var out []string

	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "HOME=") {
			out = append(out, kv)
		}
	}

	return out
}

// pathWithoutF10 keeps git and bash reachable but no installed f10.
func pathWithoutF10() string {
	var keep []string

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "f10")); err != nil {
			keep = append(keep, dir)
		}
	}

	return strings.Join(keep, string(os.PathListSeparator))
}
