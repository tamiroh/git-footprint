// Package engine drives the rules over every blob a Source yields, once.
package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamiroh/git-footprint/internal/rule"
)

// Source yields the blobs to scan and resolves a blob back to a real file.
type Source interface {
	Blobs(yield func(rule.Blob)) error
	RealPath(b rule.Blob) string // on-disk file holding this exact content, or ""
}

type Result struct {
	Findings  []rule.Finding
	Unclaimed map[string]int // ext -> count of binary blobs no rule claimed
	Scanned   int            // blobs fed
}

func (r Result) Worst() (found bool, level rule.Level) {
	for _, f := range r.Findings {
		found = true
		if l := f.Level(); l > level {
			level = l
		}
	}
	return
}

type Engine struct {
	src   Source
	rules []rule.Rule
	links bool
}

// New: links true only when the report renders hyperlinks — resolving them can
// write copies of the leaking files to a temp dir.
func New(src Source, rules []rule.Rule, links bool) *Engine {
	return &Engine{src: src, rules: rules, links: links}
}

func (e *Engine) Run() (Result, error) {
	if e.links {
		sweep(filepath.Join(os.TempDir(), "git-footprint"))
	}
	res := Result{Unclaimed: map[string]int{}}
	err := e.src.Blobs(func(b rule.Blob) {
		res.Scanned++
		e.feed(b, 0, &res)
	})
	for _, ru := range e.rules {
		res.Findings = append(res.Findings, ru.Findings()...)
	}
	return res, err
}

func (e *Engine) feed(b rule.Blob, depth int, res *Result) {
	c := &engineCtx{eng: e, depth: depth, res: res}
	for _, ru := range e.rules {
		ru.Visit(c, b)
	}
	if !c.claimed && looksBinary(b.Content) {
		res.Unclaimed[strings.ToLower(filepath.Ext(b.Path))]++
	}
}

type engineCtx struct {
	eng     *Engine
	depth   int
	res     *Result
	claimed bool
}

func (c *engineCtx) Claim() { c.claimed = true }

func (c *engineCtx) Wants(name string) bool {
	for _, ru := range c.eng.rules {
		if w, ok := ru.(rule.Wanter); ok && w.Wants(name) {
			return true
		}
	}
	return false
}

func (c *engineCtx) Inspect(b rule.Blob) {
	if c.depth >= 1 {
		return // one level of archives only
	}
	c.eng.feed(b, c.depth+1, c.res)
}

func (c *engineCtx) Link(b rule.Blob, extract bool) string {
	if !c.eng.links {
		return ""
	}
	if p := c.eng.src.RealPath(b); p != "" {
		return p
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

// extract copies a blob to $TMPDIR/git-footprint/ so a hyperlink resolves after
// the working-tree file is gone. Run sweeps the dir first.
func (e *Engine) extract(b rule.Blob) string {
	dir := filepath.Join(os.TempDir(), "git-footprint")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	base, ext := tempStem(b)
	f, err := os.CreateTemp(dir, base+"-*"+ext)
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

// tempStem strips "*" from the name since os.CreateTemp treats it as a wildcard.
func tempStem(b rule.Blob) (base, ext string) {
	name := b.Path
	if i := strings.LastIndex(name, " » "); i >= 0 {
		name = name[i+len(" » "):]
	}
	name = filepath.Base(name)
	name = strings.NewReplacer("/", "-", `\`, "-", "..", "", "*", "").Replace(name)
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
