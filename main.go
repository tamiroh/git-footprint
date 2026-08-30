// Command git-footprint reveals the personal footprint a git repository
// exposes about its contributors.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/evanoberholster/imagemeta"
)

const version = "0.1.0"

func main() {
	os.Exit(run())
}

func run() int {
	noColor := flag.Bool("no-color", false, "never colourise output")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("git-footprint", version)
		return 0
	}

	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "git was not found on PATH")
		return 2
	}

	repoArg := "."
	if flag.NArg() > 0 {
		repoArg = flag.Arg(0)
	}
	repo, err := filepath.Abs(repoArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if !isRepo(repo) {
		fmt.Fprintf(os.Stderr, "%s is not a git repository\n", repo)
		return 2
	}
	root, err := repoRoot(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !hasCommits(root) {
		fmt.Fprintln(os.Stderr, "repository has no commits yet")
		return 2
	}

	fp, err := buildFootprint(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	scan, err := scanBlobs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "blob scan incomplete:", err)
	}

	render(os.Stdout, fp, scan, root, useColor(*noColor))
	return 0
}

func useColor(noColor bool) bool {
	if noColor {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func usage() {
	fmt.Fprint(os.Stderr, `git-footprint [--no-color] [--version] [REPO]

Check what your git history reveals about you before you make a repository
public. Lists every author/committer identity in the history and the EXIF
metadata (location, creator, camera) of any image each of them committed.

REPO defaults to the current directory.
`)
}

// git

func gitRun(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return string(out), err
}

// catFileBatch streams the contents of the given blobs through one
// `git cat-file --batch` process, calling fn for each.
func catFileBatch(repo string, shas []string, fn func(sha string, content []byte)) error {
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, s := range shas {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return
			}
		}
		w.Flush()
	}()

	const maxBlob = 64 << 20
	br := bufio.NewReaderSize(stdout, 1<<16)
	for range shas {
		header, err := br.ReadString('\n')
		if err != nil {
			break
		}
		cols := strings.Fields(header)
		if len(cols) < 3 { // "<sha> missing"
			continue
		}
		size, err := strconv.Atoi(cols[2])
		if err != nil {
			continue
		}
		if size > maxBlob {
			if _, err := br.Discard(size + 1); err != nil {
				break
			}
			continue
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(br, buf); err != nil {
			break
		}
		br.ReadByte() // trailing newline
		fn(cols[0], buf)
	}
	io.Copy(io.Discard, br) // let git and the writer goroutine finish
	return cmd.Wait()
}

func gitTry(repo string, args ...string) string {
	out, _ := gitRun(repo, args...)
	return strings.TrimSpace(out)
}

func isRepo(path string) bool {
	_, err := gitRun(path, "rev-parse", "--git-dir")
	return err == nil
}

func hasCommits(repo string) bool {
	_, err := gitRun(repo, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

func repoRoot(path string) (string, error) {
	out, err := gitRun(path, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// identities

const fieldSep = "\x1f" // ASCII Unit Separator, used to split git-log --format fields

type identity struct {
	name             string
	email            string
	authorCommits    int
	committerCommits int
	firstDate        string
	lastDate         string
	isYou            bool
}

type footprint struct {
	totalCommits int
	identities   []identity
}

func collectIdentities(repo string) ([]identity, error) {
	fields := []string{"%an", "%ae", "%ad", "%cn", "%ce", "%cd"}
	// --branches --tags --remotes, not --all: keep local-only refs/stash and
	// refs/notes out of the footprint.
	out, err := gitRun(repo, "log", "HEAD", "--branches", "--tags", "--remotes",
		"--no-color", "--date=short", "--format="+strings.Join(fields, fieldSep))
	if err != nil {
		return nil, err
	}

	type key struct{ name, email string }
	byKey := map[key]*identity{}
	var order []key

	note := func(name, email, date string, author bool) {
		email = strings.ToLower(email)
		k := key{name, email}
		id := byKey[k]
		if id == nil {
			id = &identity{name: name, email: email, firstDate: date, lastDate: date}
			byKey[k] = id
			order = append(order, k)
		}
		if author {
			id.authorCommits++
		} else {
			id.committerCommits++
		}
		if len(date) == 10 {
			if id.firstDate == "" || date < id.firstDate {
				id.firstDate = date
			}
			if date > id.lastDate {
				id.lastDate = date
			}
		}
	}

	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, fieldSep)
		if len(f) < 6 {
			continue
		}
		note(f[0], f[1], f[2], true)
		note(f[3], f[4], f[5], false)
	}

	ids := make([]identity, 0, len(order))
	for _, k := range order {
		ids = append(ids, *byKey[k])
	}
	return ids, nil
}

func buildFootprint(repo string) (footprint, error) {
	youEmail := strings.ToLower(gitTry(repo, "config", "user.email"))
	youName := gitTry(repo, "config", "user.name")

	ids, err := collectIdentities(repo)
	if err != nil {
		return footprint{}, err
	}

	switch {
	case youEmail != "":
		for i := range ids {
			ids[i].isYou = ids[i].email == youEmail
		}
	case youName != "":
		for i := range ids {
			ids[i].isYou = ids[i].name == youName
		}
	}

	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i].isYou != ids[j].isYou {
			return ids[i].isYou
		}
		return ids[i].authorCommits+ids[i].committerCommits >
			ids[j].authorCommits+ids[j].committerCommits
	})

	commits := 0
	for _, id := range ids {
		commits += id.authorCommits
	}
	return footprint{totalCommits: commits, identities: ids}, nil
}

// blobs

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true,
	".png": true, ".tif": true, ".tiff": true, ".dng": true,
	".heic": true, ".heif": true, ".avif": true,
	".cr2": true, ".cr3": true, ".crw": true, ".arw": true, ".nef": true,
}

type imageMeta struct {
	path    string
	byName  string // author of the commit that introduced this blob
	byEmail string
	gps     string // "lat, long"
	creator string // Artist or Copyright
	camera  string // "Make Model"
	taken   string // "2006-01-02 15:04:05"
}

func (m imageMeta) revealing() bool { return m.gps != "" || m.creator != "" }

func (m imageMeta) empty() bool {
	return m.gps == "" && m.creator == "" && m.camera == "" && m.taken == ""
}

type blobScan struct {
	images      []imageMeta
	uninspected map[string]int // extension -> count of binary blobs nothing was read from
}

type blobRef struct {
	path, byName, byEmail string
}

// scanBlobs reads every blob ever added or changed, pulls EXIF from images, and
// tallies the binary blobs it had nothing to say about.
func scanBlobs(repo string) (blobScan, error) {
	const hdr = "\x1e"
	out, err := gitRun(repo, "log", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--format="+hdr+"%an"+fieldSep+"%ae", "--raw")
	if err != nil {
		return blobScan{}, err
	}

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

	scan := blobScan{uninspected: map[string]int{}}
	err = catFileBatch(repo, shas, func(sha string, content []byte) {
		if !looksBinary(content) {
			return
		}
		ref := bySha[sha]
		ext := strings.ToLower(filepath.Ext(ref.path))
		if imageExts[ext] {
			// a supported image type: inspected, whether or not it had EXIF
			if m := readEXIF(ref.path, content); !m.empty() {
				m.byName, m.byEmail = ref.byName, ref.byEmail
				scan.images = append(scan.images, m)
			}
			return
		}
		scan.uninspected[ext]++ // a binary format git-footprint does not read
	})

	sort.SliceStable(scan.images, func(i, j int) bool {
		if scan.images[i].revealing() != scan.images[j].revealing() {
			return scan.images[i].revealing()
		}
		return scan.images[i].path < scan.images[j].path
	})
	return scan, err
}

func looksBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

func readEXIF(path string, blob []byte) (m imageMeta) {
	m.path = path
	defer func() { _ = recover() }() // parsers can panic on hostile input

	// imagemeta's PNG scanner assumes big-endian EXIF; pull the eXIf chunk
	// ourselves and hand the raw TIFF stream to the (endian-aware) TIFF path.
	decode, src := imagemeta.Decode, bytes.NewReader(blob)
	if payload := pngEXIF(blob); payload != nil {
		decode, src = imagemeta.DecodeTiff, bytes.NewReader(payload)
	}
	x, err := decode(src)
	if err != nil {
		return m
	}

	if lat, long := x.GPS.Latitude(), x.GPS.Longitude(); lat != 0 || long != 0 {
		m.gps = fmt.Sprintf("%.5f, %.5f", lat, long)
	}
	m.creator = clean(x.IFD0.Artist)
	if m.creator == "" {
		m.creator = clean(x.IFD0.Copyright)
	}
	m.camera = cameraName(clean(x.CameraMake()), clean(x.IFD0.Model))
	if t := x.OriginalDate(); !t.IsZero() {
		m.taken = t.Format("2006-01-02 15:04:05")
	}
	return m
}

func clean(s string) string { return strings.TrimRight(s, "\x00 ") }

// pngEXIF returns the raw EXIF (TIFF) payload from a PNG's eXIf chunk, or nil.
func pngEXIF(b []byte) []byte {
	if len(b) < 8 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil
	}
	for p := 8; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		p += 8
		if n < 0 || p+n > len(b) {
			return nil
		}
		switch typ {
		case "eXIf":
			return b[p : p+n]
		case "IEND":
			return nil
		}
		p += n + 4 // chunk data + CRC
	}
	return nil
}

func cameraName(mk, model string) string {
	switch {
	case mk == "":
		return model
	case model == "":
		return mk
	}
	if brand := strings.Fields(mk); len(brand) > 0 &&
		strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand[0])) {
		return model // model already carries the maker, e.g. "NIKON D2H"
	}
	return mk + " " + model
}

// report

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

type painter struct {
	w     io.Writer
	color bool
}

func (pt painter) put(text string, codes ...string) {
	var active []string
	for _, c := range codes {
		if c != "" {
			active = append(active, c)
		}
	}
	if pt.color && len(active) > 0 {
		fmt.Fprint(pt.w, strings.Join(active, "")+text+ansiReset)
	} else {
		fmt.Fprint(pt.w, text)
	}
}

func plural(n int, one, many string) string {
	form := many
	if n == 1 {
		form = one
	}
	return strings.ReplaceAll(form, "$1", fmt.Sprint(n))
}

func commitCount(id identity) string {
	switch {
	case id.authorCommits == 0:
		return plural(id.committerCommits, "$1 commit", "$1 commits") + " (as committer only)"
	case id.committerCommits == 0:
		return plural(id.authorCommits, "$1 commit", "$1 commits") + " (as author only)"
	case id.authorCommits != id.committerCommits:
		return plural(id.authorCommits, "$1 commit", "$1 commits") +
			fmt.Sprintf(" (%d as committer)", id.committerCommits)
	default:
		return plural(id.authorCommits, "$1 commit", "$1 commits")
	}
}

func dateRange(id identity) string {
	switch {
	case len(id.firstDate) == 10 && id.firstDate != id.lastDate:
		return id.firstDate + " -> " + id.lastDate
	case len(id.firstDate) == 10:
		return id.firstDate
	default:
		return ""
	}
}

func render(w io.Writer, fp footprint, scan blobScan, repo string, color bool) {
	pt := painter{w: w, color: color}
	images := scan.images

	pt.put("git-footprint\n", ansiBold)
	pt.put("  " + repo + "\n")
	pt.put("  " + plural(fp.totalCommits, "$1 commit", "$1 commits") + " across " +
		plural(len(fp.identities), "$1 identity", "$1 identities") + "\n\n")

	byWho := map[[2]string][]imageMeta{}
	for _, m := range images {
		k := [2]string{m.byName, m.byEmail}
		byWho[k] = append(byWho[k], m)
	}

	revealing := 0
	for _, id := range fp.identities {
		youColor := ""
		if id.isYou {
			youColor = ansiGreen
		}
		pt.put("● ", ansiBold, youColor)
		if id.isYou {
			pt.put("you  ", ansiBold, ansiGreen)
		}
		pt.put(id.name+" <"+id.email+">\n", ansiBold)

		line := "    " + commitCount(id)
		if dr := dateRange(id); dr != "" {
			line += "  ·  " + dr
		}
		pt.put(line + "\n")

		k := [2]string{id.name, id.email}
		for _, m := range byWho[k] {
			if m.revealing() {
				revealing++
			}
			imageLine(pt, m)
		}
		delete(byWho, k)

		pt.put("\n")
	}

	var orphan []imageMeta
	for _, ms := range byWho {
		orphan = append(orphan, ms...)
	}
	if len(orphan) > 0 {
		sort.SliceStable(orphan, func(i, j int) bool { return orphan[i].path < orphan[j].path })
		pt.put("\nimages not tied to a listed identity\n", ansiBold)
		for _, m := range orphan {
			if m.revealing() {
				revealing++
			}
			imageLine(pt, m)
		}
	}

	if len(images) > 0 {
		s := plural(len(images), "$1 image carries EXIF metadata", "$1 images carry EXIF metadata")
		if revealing > 0 {
			pt.put(s+" ("+plural(revealing, "$1 reveals", "$1 reveal")+
				" a location or creator)\n", ansiYellow)
		} else {
			pt.put(s + "\n")
		}
	}

	if n := total(scan.uninspected); n > 0 {
		pt.put("not read (unsupported format): "+
			plural(n, "$1 binary file", "$1 binary files")+
			"  ·  "+extBreakdown(scan.uninspected)+"\n", ansiDim)
	}
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func extBreakdown(m map[string]int) string {
	type kv struct {
		ext string
		n   int
	}
	kvs := make([]kv, 0, len(m))
	for e, n := range m {
		kvs = append(kvs, kv{e, n})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].n != kvs[j].n {
			return kvs[i].n > kvs[j].n
		}
		return kvs[i].ext < kvs[j].ext
	})
	parts := make([]string, len(kvs))
	for i, k := range kvs {
		ext := k.ext
		if ext == "" {
			ext = "(no ext)"
		}
		parts[i] = fmt.Sprintf("%s ×%d", ext, k.n)
	}
	return strings.Join(parts, "  ·  ")
}

func imageLine(pt painter, m imageMeta) {
	marker, code := "● ", ""
	if m.revealing() {
		marker, code = "⚠ ", ansiYellow
	}
	var parts []string
	if m.gps != "" {
		parts = append(parts, "location "+m.gps)
	}
	if m.creator != "" {
		parts = append(parts, "creator "+m.creator)
	}
	if m.camera != "" {
		parts = append(parts, m.camera)
	}
	if m.taken != "" {
		parts = append(parts, m.taken)
	}
	pt.put("    "+marker+m.path+"  ·  "+strings.Join(parts, "  ·  ")+"\n", code)
}
