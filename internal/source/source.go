// Package source yields the blobs git-footprint scans: a git repository's
// history, or a plain directory tree when there is no usable git.
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
// the top of the report (empty when there is no history).
type Source interface {
	engine.Source
	Footprint() (identity.Footprint, error)
	Root() string
}

// Open picks a git-history source when path sits in a git repository that has
// commits (and git is installed), and a directory-tree source otherwise.
func Open(path string) (Source, error) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	if _, err := exec.LookPath("git"); err == nil && gitcmd.IsRepo(path) {
		if root, err := gitcmd.Root(path); err == nil && gitcmd.HasCommits(root) {
			return &gitSource{root: root}, nil
		}
	}
	return &treeSource{root: path}, nil
}
