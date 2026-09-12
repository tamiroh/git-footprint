// Package source yields the blobs git-footprint scans: a git repository's
// history.
package source

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/tamiroh/git-footprint/internal/engine"
	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/identity"
)

// Source is what the engine scans, plus the contributor footprint that goes at
// the top of the report.
type Source interface {
	engine.Source
	Footprint() (identity.Footprint, error)
	Root() string
}

// Open resolves path to the git repository it sits in.
func Open(path string) (Source, error) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git was not found on PATH")
	}
	if !gitcmd.IsRepo(path) {
		return nil, fmt.Errorf("%s is not a git repository", path)
	}
	root, err := gitcmd.Root(path)
	if err != nil {
		return nil, err
	}
	if !gitcmd.HasCommits(root) {
		return nil, fmt.Errorf("repository has no commits yet")
	}
	return &gitSource{root: root}, nil
}
