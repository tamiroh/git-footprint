// Package scan walks a repository's blob history and pulls the findings out of
// each blob: image, video and PDF metadata, .DS_Store names, the same from
// inside committed zip archives, and a tally of unreadable binaries.
package scan

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tamiroh/git-footprint/internal/dsstore"
	"github.com/tamiroh/git-footprint/internal/gitcmd"
	"github.com/tamiroh/git-footprint/internal/meta"
)

const (
	fieldSep    = "\x1f"    // ASCII Unit Separator
	maxZipEntry = 64 << 20  // largest zip entry to decompress
	maxZipTotal = 512 << 20 // decompression budget per archive
)

type Media struct {
	meta.Data
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
	Media       []Media
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
	return tempExtract(sha[:12]+"-"+filepath.Base(path), content)
}

// tempExtract writes content to $TMPDIR/git-footprint/<name> and returns the
// path, so a hyperlink can open bytes that are no longer in the working tree.
func tempExtract(name string, content []byte) string {
	dir := filepath.Join(os.TempDir(), "git-footprint")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	tmp := filepath.Join(dir, name)
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
			bySha[sha] = blobRef{path, name, email}
			shas = append(shas, sha)
		}
	}

	res := Result{Uninspected: map[string]int{}}
	err = gitcmd.CatFileBatch(repo, shas, func(sha string, content []byte) {
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

		if meta.Handles(ref.path) {
			// a supported type: inspected, with or without embedded metadata
			if d := meta.Read(ref.path, content); !d.Empty() {
				res.Media = append(res.Media, Media{
					Data: d, Path: ref.path, ByName: ref.byName, ByEmail: ref.byEmail,
					Disk: linkTarget(repo, ref.path, sha, content, true, head),
				})
			}
			return
		}

		if isZip(content) && scanZip(&res, ref, sha, content) {
			return
		}

		if looksBinary(content) && !meta.Inert(ref.path) {
			ext := strings.ToLower(filepath.Ext(ref.path))
			res.Uninspected[ext]++ // a binary format git-footprint does not read
		}
	})

	res.Media = dedupeMedia(res.Media)
	sort.SliceStable(res.Media, func(i, j int) bool {
		if res.Media[i].Revealing() != res.Media[j].Revealing() {
			return res.Media[i].Revealing()
		}
		return res.Media[i].Path < res.Media[j].Path
	})
	sort.SliceStable(res.DSStores, func(i, j int) bool {
		return res.DSStores[i].Path < res.DSStores[j].Path
	})
	return res, err
}

func isZip(b []byte) bool {
	return bytes.HasPrefix(b, []byte("PK\x03\x04")) || bytes.HasPrefix(b, []byte("PK\x05\x06"))
}

// scanZip inspects the media, PDF and .DS_Store entries inside a committed
// archive, attributing anything found to whoever committed the archive. Nested
// archives are not followed. It returns false if the bytes will not parse as a
// zip despite the magic, so the caller can fall back to the unread tally.
func scanZip(res *Result, ref blobRef, sha string, content []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return false
	}
	budget := int64(maxZipTotal)
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || f.UncompressedSize64 > maxZipEntry ||
			(base != ".DS_Store" && !meta.Handles(f.Name)) {
			continue
		}
		data, err := readZipEntry(f)
		if err != nil {
			continue
		}
		if budget -= int64(len(data)); budget < 0 {
			break
		}

		shown := ref.path + " » " + f.Name
		flat := strings.NewReplacer("/", "-", `\`, "-", "..", "").Replace(f.Name)
		disk := tempExtract(sha[:12]+"-"+lastRunes(flat, 100), data)

		if base == ".DS_Store" {
			if names := dsstore.Names(data); len(names) > 0 {
				res.DSStores = append(res.DSStores, DSStore{
					Path: shown, Disk: disk,
					ByName: ref.byName, ByEmail: ref.byEmail, Names: names,
				})
			}
			continue
		}
		if d := meta.Read(f.Name, data); !d.Empty() {
			res.Media = append(res.Media, Media{
				Data: d, Path: shown, ByName: ref.byName, ByEmail: ref.byEmail, Disk: disk,
			})
		}
	}
	return true
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxZipEntry))
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func dedupeMedia(in []Media) []Media {
	type key struct{ path, byName, byEmail, gps, creator, camera, software, taken string }
	seen := map[key]bool{}
	var out []Media
	for _, m := range in {
		k := key{m.Path, m.ByName, m.ByEmail, m.GPS, m.Creator, m.Camera, m.Software, m.Taken}
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
