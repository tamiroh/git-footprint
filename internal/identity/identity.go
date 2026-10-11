// Package identity builds the per-contributor identity footprint.
package identity

import (
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

// git forbids NUL in names/emails/dates, so a hostile user.name can't shift fields.
const fieldSep = "\x00"

const trailerSep = "\x1f"

type Self int

const (
	NotSelf   Self = iota
	MaybeSelf      // shares a name with a confirmed self, different address
	IsSelf         // address matches this checkout's git config
)

type Identity struct {
	Name             string
	Email            string
	AuthorCommits    int
	CommitterCommits int
	Tags             int            // annotated tags it signed as tagger
	Trailers         map[string]int // trailer key -> commits naming it there
	FirstDate        string
	LastDate         string
	Bot              bool
	Self             Self
}

type Footprint struct {
	TotalCommits int
	Identities   []Identity
}

type role int

const (
	asAuthor role = iota
	asCommitter
	asTagger
)

type collector struct {
	byKey map[[2]string]*Identity
	order [][2]string
}

func (c *collector) get(name, email, date string) *Identity {
	email = strings.ToLower(email)
	k := [2]string{name, email}
	id := c.byKey[k]
	if id == nil {
		id = &Identity{Name: name, Email: email}
		c.byKey[k] = id
		c.order = append(c.order, k)
	}
	if len(date) == 10 { // "yyyy-mm-dd" from --date=short
		if id.FirstDate == "" || date < id.FirstDate {
			id.FirstDate = date
		}
		if date > id.LastDate {
			id.LastDate = date
		}
	}
	return id
}

func (c *collector) note(name, email, date string, as role) {
	id := c.get(name, email, date)
	switch as {
	case asAuthor:
		id.AuthorCommits++
	case asCommitter:
		id.CommitterCommits++
	case asTagger:
		id.Tags++
	}
}

func (c *collector) trailer(key, name, email, date string) {
	id := c.get(name, email, date)
	if id.Trailers == nil {
		id.Trailers = map[string]int{}
	}
	id.Trailers[key]++
}

func collect(repo string) ([]Identity, error) {
	fields := []string{"%an", "%ae", "%ad", "%cn", "%ce", "%cd",
		"%(trailers:only=true,unfold=true,separator=%x1f)"}
	// not --all: that would pull in refs/stash and refs/notes.
	out, err := gitcmd.Run(repo, "log", "HEAD", "--branches", "--tags", "--remotes",
		"--no-color", "--date=short", "--format="+strings.Join(fields, "%x00"))
	if err != nil {
		return nil, err
	}

	c := &collector{byKey: map[[2]string]*Identity{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, fieldSep, len(fields))
		if len(f) < len(fields) {
			continue
		}
		c.note(f[0], f[1], f[2], asAuthor)
		c.note(f[3], f[4], f[5], asCommitter)
		for _, t := range strings.Split(f[6], trailerSep) {
			if key, name, email, ok := parseTrailer(t); ok {
				c.trailer(key, name, email, f[2])
			}
		}
	}

	// Lightweight tags have no tagger and come back with empty fields.
	tags, err := gitcmd.Run(repo, "for-each-ref", "refs/tags",
		"--format=%(taggername)%00%(taggeremail)%00%(taggerdate:short)")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(tags, "\n") {
		f := strings.Split(line, fieldSep)
		if len(f) < 3 || f[1] == "" {
			continue
		}
		email := strings.TrimSuffix(strings.TrimPrefix(f[1], "<"), ">")
		c.note(f[0], email, f[2], asTagger)
	}

	ids := make([]Identity, 0, len(c.order))
	for _, k := range c.order {
		ids = append(ids, *c.byKey[k])
	}
	return ids, nil
}

// parseTrailer accepts any trailer whose value is "Name <email>", whatever its
// key: Co-authored-by, Signed-off-by, Reviewed-by and the rest all name people.
func parseTrailer(line string) (key, name, email string, ok bool) {
	key, value, found := strings.Cut(line, ":")
	if !found {
		return "", "", "", false
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	i := strings.LastIndexByte(value, '<')
	if key == "" || i < 1 || !strings.HasSuffix(value, ">") {
		return "", "", "", false
	}
	name, email = strings.TrimSpace(value[:i]), value[i+1:len(value)-1]
	// keeps "Link: see <https://...>" from reading as a person
	if name == "" || !strings.Contains(email, "@") || strings.Contains(email, "://") || strings.ContainsAny(email, " <>") {
		return "", "", "", false
	}
	// git matches trailer keys case-insensitively
	key = strings.ToUpper(key[:1]) + strings.ToLower(key[1:])
	return key, name, email, true
}

func Build(repo string) (Footprint, error) {
	ids, err := collect(repo)
	if err != nil {
		return Footprint{}, err
	}

	for i := range ids {
		ids[i].Bot = looksBot(ids[i].Name, ids[i].Email)
	}
	markSelf(ids, repo)

	sort.SliceStable(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if (a.Self != NotSelf) != (b.Self != NotSelf) {
			return a.Self != NotSelf // you and maybe-you first
		}
		if a.Self != b.Self {
			return a.Self > b.Self // (you) before (maybe you)
		}
		if a.Bot != b.Bot {
			return !a.Bot
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.Email < b.Email
	})

	commits := 0
	for _, id := range ids {
		commits += id.AuthorCommits
	}
	return Footprint{TotalCommits: commits, Identities: ids}, nil
}

// markSelf never links people who aren't you: it matches only against this
// checkout's own git config.
func markSelf(ids []Identity, repo string) {
	cfgEmail := strings.ToLower(gitcmd.Try(repo, "config", "user.email"))
	cfgName := gitcmd.Try(repo, "config", "user.name")

	for i := range ids {
		switch {
		case cfgEmail != "" && strings.EqualFold(ids[i].Email, cfgEmail):
			ids[i].Self = IsSelf
		case cfgEmail == "" && cfgName != "" && strings.EqualFold(ids[i].Name, cfgName):
			ids[i].Self = IsSelf
		}
	}

	selfNames := map[string]bool{}
	if cfgName != "" {
		selfNames[strings.ToLower(cfgName)] = true
	}
	for _, id := range ids {
		if id.Self == IsSelf {
			selfNames[strings.ToLower(id.Name)] = true
		}
	}
	for i := range ids {
		if ids[i].Self == NotSelf && selfNames[strings.ToLower(ids[i].Name)] {
			ids[i].Self = MaybeSelf
		}
	}
}

// looksBot is a literal string match, never a guess about a person.
func looksBot(name, email string) bool {
	return strings.HasSuffix(name, "[bot]") ||
		name == "web-flow" ||
		email == "noreply@github.com"
}
