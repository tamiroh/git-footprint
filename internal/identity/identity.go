// Package identity builds the per-contributor identity footprint of a
// repository's history.
package identity

import (
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

// fieldSep is NUL: git forbids it in names, emails and dates, so a hostile
// user.name can't shift the parsed fields. The format string emits it as %x00.
const fieldSep = "\x00"

// Self is how confidently an identity belongs to whoever is running the tool.
type Self int

const (
	NotSelf   Self = iota
	MaybeSelf      // same name as a confirmed self, different address
	IsSelf         // address matches this checkout's git config user.email
)

type Identity struct {
	Name             string
	Email            string
	AuthorCommits    int
	CommitterCommits int
	FirstDate        string
	LastDate         string
	Bot              bool // name ends in "[bot]" or commits come from a CI/web service
	Self             Self
}

type Footprint struct {
	TotalCommits int
	Identities   []Identity
}

func collect(repo string) ([]Identity, error) {
	fields := []string{"%an", "%ae", "%ad", "%cn", "%ce", "%cd"}
	// --branches --tags --remotes, not --all: keep local-only refs/stash and
	// refs/notes out of the footprint.
	out, err := gitcmd.Run(repo, "log", "HEAD", "--branches", "--tags", "--remotes",
		"--no-color", "--date=short", "--format="+strings.Join(fields, "%x00"))
	if err != nil {
		return nil, err
	}

	type key struct{ name, email string }
	byKey := map[key]*Identity{}
	var order []key

	note := func(name, email, date string, author bool) {
		email = strings.ToLower(email)
		k := key{name, email}
		id := byKey[k]
		if id == nil {
			id = &Identity{Name: name, Email: email}
			byKey[k] = id
			order = append(order, k)
		}
		if author {
			id.AuthorCommits++
		} else {
			id.CommitterCommits++
		}
		if len(date) == 10 { // "--date=short"; ignore anything else
			if id.FirstDate == "" || date < id.FirstDate {
				id.FirstDate = date
			}
			if date > id.LastDate {
				id.LastDate = date
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

	ids := make([]Identity, 0, len(order))
	for _, k := range order {
		ids = append(ids, *byKey[k])
	}
	return ids, nil
}

// Build assembles the identity footprint. The entries that are (or might be)
// you sort first; the rest go by name then email so a person's aliases sit
// adjacent, with bots last.
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

// markSelf flags identities against this checkout's git config: an exact
// user.email match (or, if that is unset, a user.name match) is IsSelf; any
// other identity sharing a name with a confirmed self is MaybeSelf. It never
// groups people who aren't you.
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

// looksBot reports whether an identity is lexically a bot or service account:
// GitHub appends "[bot]" to bot account names, web-UI commits are committed by
// "GitHub <noreply@github.com>", and merges by "web-flow". No guess about
// people is made.
func looksBot(name, email string) bool {
	return strings.HasSuffix(name, "[bot]") ||
		name == "web-flow" ||
		email == "noreply@github.com"
}
