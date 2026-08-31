// Package rule is the detection contract: a Rule inspects every blob and reports
// Findings.
package rule

type Level int

const (
	Info Level = iota
	Warn
)

type Author struct{ Name, Email string }

type Blob struct {
	Path    string // "outer.zip » inner/x.jpg" for an archive entry
	Name    string // matched for type; the bare entry name inside an archive
	Content []byte
	By      Author
	SHA     string
	depth   int
}

type Check struct {
	Name  string
	Level Level
	Value string
}

type Finding struct {
	Detector string
	Path     string
	Link     string
	By       Author
	Checks   []Check
	Count    int
}

func NonEmpty(cs []Check) []Check {
	out := make([]Check, 0, len(cs))
	for _, c := range cs {
		if c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

func (f Finding) Level() Level {
	l := Info
	for _, c := range f.Checks {
		if c.Level > l {
			l = c.Level
		}
	}
	return l
}

type Context interface {
	Claim()                           // recognised: keep it out of the unread tally
	Inspect(Blob)                     // re-run the rules on a nested blob, one level deep
	Link(b Blob, extract bool) string // working-tree file, temp copy, or ""
	Wants(name string) bool
}

type Rule interface {
	Visit(ctx Context, b Blob)
	Findings() []Finding
}

type Wanter interface {
	Wants(name string) bool
}
