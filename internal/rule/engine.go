package rule

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

// Result is everything the walk produced.
type Result struct {
	Findings  []Finding
	Unclaimed map[string]int // extension -> count of binary blobs no rule claimed
}

// Worst reports whether there are any findings and the highest level among them.
func (r Result) Worst() (found bool, level Level) {
	for _, f := range r.Findings {
		found = true
		if f.Level > level {
			level = f.Level
		}
	}
	return
}

// Engine walks a repository's blob history once and drives the rules over it.
type Engine struct {
	root  string
	rules []Rule
	head  map[string]string
	links bool // extract historical bytes so hyperlinks resolve (interactive only)
	swept bool
}

// NewEngine builds the engine. links should be true only when the report will
// render clickable hyperlinks, since resolving them extracts copies of the
// leaking files to a temp dir.
func NewEngine(root string, rules []Rule, links bool) *Engine {
	return &Engine{root: root, rules: rules, head: gitcmd.HeadBlobs(root), links: links}
}

const (
	logRec   = "\x1e" // record separator, prefixes each commit header line
	fieldSep = "\x1f" // unit separator, between %an and %ae
)

// Run walks every blob ever added or changed and returns the collected findings.
func (e *Engine) Run() (Result, error) {
	out, err := gitcmd.Run(e.root, "-c", "core.quotePath=false",
		"log", "HEAD", "--branches", "--tags", "--remotes",
		"--reverse", "--no-renames", "--no-abbrev", "--diff-filter=AM",
		"--no-color", "--format="+logRec+"%an"+fieldSep+"%ae", "--raw")
	if err != nil {
		return Result{}, err
	}

	type ref struct {
		path string
		by   Author
	}
	bySha := map[string]ref{}
	var shas []string
	var by Author
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, logRec):
			if f := strings.Split(line[len(logRec):], fieldSep); len(f) >= 2 {
				by = Author{f[0], strings.ToLower(f[1])}
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

	res := Result{Unclaimed: map[string]int{}}
	err = gitcmd.CatFileBatch(e.root, shas, func(sha string, content []byte) {
		r := bySha[sha]
		e.feed(Blob{Path: r.path, Name: r.path, Content: content, By: r.by, SHA: sha}, &res)
	})
	for _, ru := range e.rules {
		res.Findings = append(res.Findings, ru.Findings()...)
	}
	return res, err
}

func (e *Engine) feed(b Blob, res *Result) {
	c := &engineCtx{eng: e, blob: b, res: res}
	for _, ru := range e.rules {
		ru.Visit(c, b)
	}
	if !c.claimed && looksBinary(b.Content) {
		res.Unclaimed[strings.ToLower(filepath.Ext(b.Path))]++
	}
}

type engineCtx struct {
	eng     *Engine
	blob    Blob
	res     *Result
	claimed bool
}

func (c *engineCtx) Claim() { c.claimed = true }

func (c *engineCtx) Wants(name string) bool {
	for _, ru := range c.eng.rules {
		if w, ok := ru.(Wanter); ok && w.Wants(name) {
			return true
		}
	}
	return false
}

func (c *engineCtx) Inspect(b Blob) {
	if c.blob.depth >= 1 {
		return // one level of archives only
	}
	b.depth = c.blob.depth + 1
	c.eng.feed(b, c.res)
}

func (c *engineCtx) Link(b Blob, extract bool) string {
	if !c.eng.links {
		return ""
	}
	if b.SHA != "" && c.eng.head[b.Path] == b.SHA {
		return filepath.Join(c.eng.root, b.Path)
	}
	if !extract {
		return ""
	}
	return c.eng.extract(b)
}

func looksBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

const tempTTL = time.Hour

// extract writes a blob's bytes to a fresh private file under
// $TMPDIR/git-footprint/ so a hyperlink can open bytes no longer in the working
// tree. Files older than one run are swept first; anything left is a copy of a
// leaking file, so the name carries no more than the report already shows.
func (e *Engine) extract(b Blob) string {
	dir := filepath.Join(os.TempDir(), "git-footprint")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	if !e.swept {
		e.swept = true
		sweep(dir)
	}
	base, ext := tempStem(b)
	f, err := os.CreateTemp(dir, base+"-*"+ext) // 0600, unpredictable suffix
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(b.Content); err != nil {
		os.Remove(f.Name())
		return ""
	}
	return f.Name()
}

// tempStem splits a recognisable prefix and the extension for the temp name.
func tempStem(b Blob) (base, ext string) {
	name := b.Path
	if i := strings.LastIndex(name, " » "); i >= 0 {
		name = name[i+len(" » "):]
	}
	name = filepath.Base(name)
	name = strings.NewReplacer("/", "-", `\`, "-", "..", "").Replace(name)
	ext = filepath.Ext(name)
	name = strings.TrimSuffix(name, ext)
	if r := []rune(name); len(r) > 60 {
		name = string(r[len(r)-60:])
	}
	if len(b.SHA) >= 12 {
		name = b.SHA[:12] + "-" + name
	}
	return name, ext
}

func sweep(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > tempTTL {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
