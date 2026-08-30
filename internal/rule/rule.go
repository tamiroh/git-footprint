// Package rule is the detection contract: a Rule inspects every blob in a
// repository's history and reports Findings.
package rule

type Level int // maps to the [INFO] / [WARN] tag

const (
	Info Level = iota
	Warn
)

type Author struct{ Name, Email string }

type Blob struct {
	Path    string // display path; an archive entry reads "outer.zip » inner/x.jpg"
	Name    string // what to match the type on; the entry name when inside an archive
	Content []byte
	By      Author
	SHA     string
	depth   int
}

type Field struct{ Label, Value string } // one detail line; empty Label prints the value alone

type Finding struct {
	Rule   string
	Level  Level
	Path   string
	Link   string
	By     Author
	Detail []Field
	Count  int // rule-specific magnitude (e.g. names in a .DS_Store)
}

type Context interface {
	Claim()                           // recognise the blob's format, so it isn't tallied as unread
	Inspect(Blob)                     // feed a nested blob back through the rules (one level only)
	Link(b Blob, extract bool) string // hyperlink target: working-tree file, temp copy, or ""
	Wants(name string) bool           // would any rule inspect a file with this name? (archive filter)
}

// Rule detects one kind of finding. Visit runs per blob; Findings runs once
// after, so a rule can dedupe or aggregate first.
type Rule interface {
	Name() string
	Visit(ctx Context, b Blob)
	Findings() []Finding
}

// Wanter is the optional half of Rule that lets the archive rule skip entries no
// one cares about without decompressing them.
type Wanter interface {
	Wants(name string) bool
}
