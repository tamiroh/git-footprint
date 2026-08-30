package rule

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
}

func NewEngine(root string, rules []Rule) *Engine {
	return &Engine{root: root, rules: rules, head: gitcmd.HeadBlobs(root)}
}

const (
	logRec   = "\x1e" // record separator, prefixes each commit header line
	fieldSep = "\x1f" // unit separator, between %an and %ae
)

// Run walks every blob ever added or changed and returns the collected findings.
func (e *Engine) Run() (Result, error) {
	out, err := gitcmd.Run(e.root, "-c", "core.quotePath=false",
		"log", "--branches", "--tags", "--remotes",
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

func (c *engineCtx) Inspect(b Blob) {
	if c.blob.depth >= 1 {
		return // one level of archives only
	}
	b.depth = c.blob.depth + 1
	c.eng.feed(b, c.res)
}

func (c *engineCtx) Link(b Blob, extract bool) string {
	if b.SHA != "" && c.eng.head[b.Path] == b.SHA {
		return filepath.Join(c.eng.root, b.Path)
	}
	if !extract {
		return ""
	}
	return tempExtract(tempName(b), b.Content)
}

func looksBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// tempName is the basename for a temp extract: the sha12 prefix keeps copies of
// distinct blobs apart; an archive entry's sub-path is flattened.
func tempName(b Blob) string {
	name := b.Path
	if i := strings.LastIndex(name, " » "); i >= 0 {
		name = name[i+len(" » "):]
	} else {
		name = filepath.Base(name)
	}
	name = strings.NewReplacer("/", "-", `\`, "-", "..", "").Replace(name)
	if r := []rune(name); len(r) > 100 {
		name = string(r[len(r)-100:])
	}
	if len(b.SHA) >= 12 {
		return b.SHA[:12] + "-" + name
	}
	return name
}

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
