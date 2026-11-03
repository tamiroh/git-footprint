package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var updateSnapshots = flag.Bool("update", false, "update CLI snapshots")

func TestCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "git-footprint")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	paths, err := filepath.Glob("testdata/cli/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no CLI cases found")
	}
	for _, casePath := range paths {
		t.Run(filepath.Base(filepath.Dir(casePath)), func(t *testing.T) {
			data, err := os.ReadFile(casePath)
			if err != nil {
				t.Fatal(err)
			}
			var tc cliCase
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&tc); err != nil {
				t.Fatal(err)
			}
			repo, env := newFixtureRepo(t, tc.Steps)
			args := append([]string{"--no-color", "--no-pager"}, tc.Args...)
			cmd := exec.Command(binary, append(args, repo)...)
			cmd.Dir, cmd.Env = repo, env
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			exitCode := 0
			if err := cmd.Run(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.Exit {
				t.Fatalf("exit = %d, want %d\nstdout:\n%sstderr:\n%s", exitCode, tc.Exit, &stdout, &stderr)
			}
			got := fmt.Sprintf("exit: %d\nstdout:\n%sstderr:\n%s", exitCode, &stdout, &stderr)
			path := filepath.Join(filepath.Dir(casePath), "expected.golden")
			if *updateSnapshots {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("CLI snapshot mismatch (-want +got):\n%s\nTo update: go test . -run '^TestCLI$' -update", diff)
			}
		})
	}
}

type cliCase struct {
	Args  []string      `json:"args"`
	Exit  int           `json:"exit"`
	Steps []fixtureStep `json:"steps"`
}

type fixtureStep struct {
	Git   []string        `json:"git"`
	Write *fixtureFile    `json:"write"`
	Zip   *fixtureArchive `json:"zip"`
}

type fixtureFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fixtureArchive struct {
	Path    string            `json:"path"`
	Entries map[string]string `json:"entries"`
}

func newFixtureRepo(t *testing.T, steps []fixtureStep) (string, []string) {
	t.Helper()
	repo := t.TempDir()
	// Keep process/tool paths, but discard all inherited Git settings and identities.
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") || key == "HOME" || key == "XDG_CONFIG_HOME" || key == "EMAIL" || key == "TZ" || key == "LC_ALL" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fixture Author", "GIT_AUTHOR_EMAIL=author@example.com",
		"GIT_COMMITTER_NAME=Fixture Author", "GIT_COMMITTER_EMAIL=author@example.com",
		"GIT_AUTHOR_DATE=2020-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2020-01-02T03:04:05Z",
		"TZ=UTC", "LC_ALL=C",
	)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repo, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--initial-branch=main", "--template="+t.TempDir())
	git("config", "user.name", "Fixture Author")
	git("config", "user.email", "author@example.com")
	for i, step := range steps {
		operations := 0
		if len(step.Git) > 0 {
			operations++
		}
		if step.Write != nil {
			operations++
		}
		if step.Zip != nil {
			operations++
		}
		if operations != 1 {
			t.Fatalf("step %d must contain exactly one of git, write, zip", i+1)
		}
		switch {
		case len(step.Git) > 0:
			git(step.Git...)
		case step.Write != nil:
			writeFixtureFile(t, repo, step.Write.Path, []byte(step.Write.Content))
		case step.Zip != nil:
			writeFixtureFile(t, repo, step.Zip.Path, fixtureZIP(t, step.Zip.Entries))
		}
	}
	return repo, env
}

func writeFixtureFile(t *testing.T, repo, name string, content []byte) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
