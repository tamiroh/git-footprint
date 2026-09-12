package source

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/rule"
)

// treeSource walks the files under root, for a directory that is not a git
// repository. No history, so no per-contributor attribution.
type treeSource struct{ root string }

func (t *treeSource) Root() string { return t.root }

// Footprint is empty: without history there are no contributors to attribute.
func (t *treeSource) Footprint() (identity.Footprint, error) { return identity.Footprint{}, nil }

const maxFile = 64 << 20 // matches gitcmd's per-blob cap

func (t *treeSource) Blobs(yield func(rule.Blob)) error {
	return filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		if d.IsDir() {
			if p != t.root && d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlink, socket, device
		}
		if info, err := d.Info(); err != nil || info.Size() > maxFile {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		yield(rule.Blob{Path: rel, Name: rel, Content: content})
		return nil
	})
}

func (t *treeSource) RealPath(b rule.Blob) string {
	if strings.Contains(b.Path, " » ") { // an archive entry, not a file on disk
		return ""
	}
	return filepath.Join(t.root, filepath.FromSlash(b.Path))
}
