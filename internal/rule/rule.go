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

// Check is one named, individually-levelled thing a detector noticed in a blob
// (ESLint-style): "image-location", "office-author". The name is the identifier
// a future config toggles; Value is what would leak.
type Check struct {
	Name  string
	Level Level
	Value string
}

// Finding is everything one detector found in one blob. A blob can trip several
// checks (a photo with GPS, a camera model and an editor name); the report shows
// them as one block and takes the finding's level from its worst check.
type Finding struct {
	Detector string
	Path     string
	Link     string
	By       Author
	Checks   []Check
	Count    int // detector-specific magnitude (e.g. names in a .DS_Store)
}

// NonEmpty returns the checks whose value is set, in order — the detectors build
// a fixed list of candidate checks and let this drop the fields that were blank.
func NonEmpty(cs []Check) []Check {
	out := make([]Check, 0, len(cs))
	for _, c := range cs {
		if c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

// Level is the finding's worst check level, or Info when it has no checks.
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
	Claim()                           // recognise the blob's format, so it isn't tallied as unread
	Inspect(Blob)                     // feed a nested blob back through the rules (one level only)
	Link(b Blob, extract bool) string // hyperlink target: working-tree file, temp copy, or ""
	Wants(name string) bool           // would any rule inspect a file with this name? (archive filter)
}

// Rule is a detector: Visit runs per blob to accumulate, Findings runs once
// after so the rule can dedupe or aggregate first. Each Finding it emits names
// its detector and carries one or more individually-levelled checks.
type Rule interface {
	Visit(ctx Context, b Blob)
	Findings() []Finding
}

// Wanter is the optional half of Rule that lets the archive rule skip entries no
// one cares about without decompressing them.
type Wanter interface {
	Wants(name string) bool
}
