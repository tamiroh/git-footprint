// Package scan walks a repository's blob history and pulls the findings out of
// each blob: image EXIF, .DS_Store names, and a tally of unreadable binaries.
package scan

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tamiroh/git-footprint/internal/dsstore"
	"github.com/tamiroh/git-footprint/internal/exif"
	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

const fieldSep = "\x1f" // ASCII Unit Separator

type Image struct {
	exif.Data
	Path    string
	Disk    string // abs path a hyperlink should open (working tree, or a temp extract)
	ByName  string // author of the commit that introduced this blob
	ByEmail string
}

type DSStore struct {
	Path            string
	Disk            string
	ByName, ByEmail string
	Names           []string
}

type Result struct {
	Images      []Image
	DSStores    []DSStore
	Uninspected map[string]int // extension -> count of binary blobs nothing was read from
}

type blobRef struct {
	path, byName, byEmail string
}

// linkTarget is the absolute path a finding's hyperlink should open. When the
// working tree holds exactly the bytes the finding came from, that file;
// otherwise (for extractable types) a copy of the historical bytes under the
// temp dir. "" means no link.
func linkTarget(root, path, sha string, content []byte, extract bool, head map[string]string) string {
	if head[path] == sha {
		return filepath.Join(root, path)
	}
	if !extract {
		return ""
	}
	dir := filepath.Join(os.TempDir(), "git-footprint")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	tmp := filepath.Join(dir, sha[:12]+"-"+filepath.Base(path))
	if _, err := os.Stat(tmp); err != nil {
		if os.WriteFile(tmp, content, 0o644) != nil {
			return ""
		}
	}
	return tmp
}

// Blobs reads every blob ever added or changed in the history.
func Blobs(repo string) (Result, error) {
	const hdr = "\x1e"
	out, err := gitcmd.Run(repo, "-c", "core.quotePath=false",
		"log", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--format="+hdr+"%an"+fieldSep+"%ae", "--raw")
	if err != nil {
		return Result{}, err
	}
	head := gitcmd.HeadBlobs(repo)

	bySha := map[string]blobRef{}
	var shas []string
	var name, email string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, hdr):
			if f := strings.Split(line[len(hdr):], fieldSep); len(f) >= 2 {
				name, email = f[0], strings.ToLower(f[1])
			}
		case strings.HasPrefix(line, ":"):
			meta, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			if strings.HasPrefix(path, `"`) {
				if uq, e := strconv.Unquote(path); e == nil {
					path = uq
				}
			}
			cols := strings.Fields(meta)
			if len(cols) < 5 {
				continue
			}
			sha := cols[3]
			if _, dup := bySha[sha]; dup || strings.Trim(sha, "0") == "" {
				continue
			}
			bySha[sha] = blobRef{path, name, email}
			shas = append(shas, sha)
		}
	}

	res := Result{Uninspected: map[string]int{}}
	err = gitcmd.CatFileBatch(repo, shas, func(sha string, content []byte) {
		if !looksBinary(content) {
			return
		}
		ref := bySha[sha]

		if filepath.Base(ref.path) == ".DS_Store" {
			if names := dsstore.Names(content); len(names) > 0 {
				res.DSStores = append(res.DSStores, DSStore{
					Path: ref.path, Disk: linkTarget(repo, ref.path, sha, content, false, head),
					ByName: ref.byName, ByEmail: ref.byEmail, Names: names,
				})
			}
			return
		}

		ext := strings.ToLower(filepath.Ext(ref.path))
		if exif.IsImage(ref.path) {
			// a supported image type: inspected, whether or not it had EXIF
			if d := exif.Read(content); !d.Empty() {
				res.Images = append(res.Images, Image{
					Data: d, Path: ref.path, ByName: ref.byName, ByEmail: ref.byEmail,
					Disk: linkTarget(repo, ref.path, sha, content, true, head),
				})
			}
			return
		}
		res.Uninspected[ext]++ // a binary format git-footprint does not read
	})

	res.Images = dedupeImages(res.Images)
	sort.SliceStable(res.Images, func(i, j int) bool {
		if res.Images[i].Revealing() != res.Images[j].Revealing() {
			return res.Images[i].Revealing()
		}
		return res.Images[i].Path < res.Images[j].Path
	})
	sort.SliceStable(res.DSStores, func(i, j int) bool {
		return res.DSStores[i].Path < res.DSStores[j].Path
	})
	return res, err
}

func dedupeImages(in []Image) []Image {
	type key struct{ path, byName, byEmail, gps, creator, camera, taken string }
	seen := map[key]bool{}
	var out []Image
	for _, m := range in {
		k := key{m.Path, m.ByName, m.ByEmail, m.GPS, m.Creator, m.Camera, m.Taken}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

func looksBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}
