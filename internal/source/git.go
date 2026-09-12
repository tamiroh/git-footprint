package source

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/identity"
	"github.com/tamiroh/git-footprint/internal/rule"
)

// gitSource walks every blob ever added or modified across all local refs.
type gitSource struct {
	root string
	head map[string]string // HEAD path -> blob sha, loaded on first RealPath call
}

func (g *gitSource) Root() string { return g.root }

func (g *gitSource) Footprint() (identity.Footprint, error) { return identity.Build(g.root) }

const (
	logRec   = "\x1e"
	fieldSep = "\x00" // git forbids NUL in name/email, so a hostile name can't split the line
)

func (g *gitSource) Blobs(yield func(rule.Blob)) error {
	out, err := gitcmd.Run(g.root, "-c", "core.quotePath=false",
		"log", "HEAD", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--format=%x1e%an%x00%ae", "--raw")
	if err != nil {
		return err
	}

	type ref struct {
		path string
		by   rule.Author
	}
	bySha := map[string]ref{}
	var shas []string
	var by rule.Author
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, logRec):
			if f := strings.Split(line[len(logRec):], fieldSep); len(f) >= 2 {
				by = rule.Author{Name: f[0], Email: strings.ToLower(f[1])}
			}
		case strings.HasPrefix(line, ":"):
			info, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			if strings.HasPrefix(path, `"`) {
				if uq, e := strconv.Unquote(path); e == nil {
					path = uq
				}
			}
			cols := strings.Fields(info)
			if len(cols) < 5 {
				continue
			}
			sha := cols[3]
			if _, dup := bySha[sha]; dup || strings.Trim(sha, "0") == "" {
				continue
			}
			bySha[sha] = ref{path, by}
			shas = append(shas, sha)
		}
	}

	return gitcmd.CatFileBatch(g.root, shas, func(sha string, content []byte) {
		r := bySha[sha]
		yield(rule.Blob{Path: r.path, Name: r.path, Content: content, By: r.by, SHA: sha})
	})
}

func (g *gitSource) RealPath(b rule.Blob) string {
	if b.SHA == "" { // an archive entry, not a standalone blob
		return ""
	}
	if g.head == nil {
		g.head = gitcmd.HeadBlobs(g.root)
	}
	if g.head[b.Path] == b.SHA {
		return filepath.Join(g.root, b.Path)
	}
	return ""
}
