// Package identity builds the per-contributor identity footprint.
package identity

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

// git forbids NUL in names/emails/dates, so a hostile user.name can't shift fields.
const fieldSep = "\x00"

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
	CoAuthorCommits  int
	CommitterCommits int
	FirstDate        string
	LastDate         string
	Bot              bool
	Self             Self
}

type Footprint struct {
	TotalCommits int
	Identities   []Identity
	Mentions     []Mention
}

func collect(repo string) (Footprint, error) {
	fields := []string{"%an", "%ae", "%ad", "%cn", "%ce", "%cd", "%H", "%(trailers)", "%(trailers:only,unfold,key_value_separator=%x3A)", "%B"}
	// not --all: that would pull in refs/stash and refs/notes.
	out, err := gitcmd.Run(repo, "log", "HEAD", "--branches", "--tags", "--remotes",
		"--no-color", "--no-notes", "--no-show-signature", "--log-size", "--date=short",
		"--format="+strings.Join(fields, "%x00"))
	if err != nil {
		return Footprint{}, err
	}

	type key struct{ name, email string }
	byKey := map[key]*Identity{}
	var order []key

	const (
		authored = iota
		committed
		coAuthored
	)
	note := func(name, email, date string, role int) {
		email = strings.ToLower(email)
		k := key{name, email}
		id := byKey[k]
		if id == nil {
			id = &Identity{Name: name, Email: email}
			byKey[k] = id
			order = append(order, k)
		}
		switch role {
		case authored:
			id.AuthorCommits++
		case committed:
			id.CommitterCommits++
		case coAuthored:
			id.CoAuthorCommits++
		}
		if len(date) == 10 { // "yyyy-mm-dd" from --date=short
			if id.FirstDate == "" || date < id.FirstDate {
				id.FirstDate = date
			}
			if date > id.LastDate {
				id.LastDate = date
			}
		}
	}

	separators := gitcmd.Try(repo, "config", "trailer.separators")
	if separators == "" {
		separators = ":"
	}
	var fp Footprint
	for out != "" {
		// Length framing keeps arbitrary message bytes from splitting commits.
		header, rest, ok := strings.Cut(out, "\n")
		size, err := strconv.Atoi(strings.TrimPrefix(header, "log size "))
		if !ok || !strings.HasPrefix(header, "log size ") || err != nil || size < 0 || size > len(rest) {
			return Footprint{}, fmt.Errorf("invalid git log record size")
		}
		f := strings.SplitN(rest[:size], fieldSep, len(fields))
		if len(f) != len(fields) {
			return Footprint{}, fmt.Errorf("incomplete git log record")
		}
		out = strings.TrimLeft(rest[size:], "\n")
		fp.TotalCommits++
		note(f[0], f[1], f[2], authored)
		note(f[3], f[4], f[5], committed)
		seen := map[key]bool{}
		for _, line := range strings.Split(f[8], "\n") {
			trailer, value, ok := strings.Cut(line, ":")
			if !ok || !strings.EqualFold(trailer, "Co-Authored-by") {
				continue
			}
			name, email, ok := parseCoAuthor(value)
			k := key{name, email}
			if ok && !seen[k] {
				note(name, email, f[2], coAuthored)
				seen[k] = true
			}
		}
		fp.Mentions = append(fp.Mentions, messageMentions(f[6], f[9], f[7], f[8], separators)...)
	}

	ids := make([]Identity, 0, len(order))
	for _, k := range order {
		ids = append(ids, *byKey[k])
	}
	fp.Identities = ids
	return fp, nil
}

// parseCoAuthor accepts Git's Name <email> identity form without interpreting
// names as RFC mail headers or requiring a public email domain.
func parseCoAuthor(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	name, email, ok := strings.Cut(value, "<")
	if !ok || !strings.HasSuffix(email, ">") {
		return "", "", false
	}
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(email, ">")))
	if name == "" || email == "" || strings.ContainsAny(name+email, "<>\x00\r\n") {
		return "", "", false
	}
	return name, email, true
}

func Build(repo string) (Footprint, error) {
	fp, err := collect(repo)
	if err != nil {
		return Footprint{}, err
	}

	ids := fp.Identities
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

	return fp, nil
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
