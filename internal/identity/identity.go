// Package identity builds the per-contributor identity footprint of a
// repository's history.
package identity

import (
	"sort"
	"strings"

	"github.com/tamiroh/git-footprint/internal/gitcmd"
)

const fieldSep = "\x1f" // ASCII Unit Separator, used to split git-log --format fields

type Identity struct {
	Name             string
	Email            string
	AuthorCommits    int
	CommitterCommits int
	FirstDate        string
	LastDate         string
	Bot              bool // name ends in "[bot]" or commits come from a CI/web service
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
		"--no-color", "--date=short", "--format="+strings.Join(fields, fieldSep))
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
			id = &Identity{Name: name, Email: email, FirstDate: date, LastDate: date}
			byKey[k] = id
			order = append(order, k)
		}
		if author {
			id.AuthorCommits++
		} else {
			id.CommitterCommits++
		}
		if len(date) == 10 {
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

// Build assembles the identity footprint. It makes no claim about which
// identities belong to the same person or to you: every distinct (name, email)
// is one entry, sorted by name then email so a person's aliases sit adjacent.
// Bot identities sort last.
func Build(repo string) (Footprint, error) {
	ids, err := collect(repo)
	if err != nil {
		return Footprint{}, err
	}

	for i := range ids {
		ids[i].Bot = looksBot(ids[i].Name, ids[i].Email)
	}

	sort.SliceStable(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
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

// looksBot reports whether an identity is lexically a bot or service account:
// GitHub appends "[bot]" to bot account names, web-UI commits are committed by
// "GitHub <noreply@github.com>", and merges by "web-flow". No guess about
// people is made.
func looksBot(name, email string) bool {
	return strings.HasSuffix(name, "[bot]") ||
		name == "web-flow" ||
		email == "noreply@github.com"
}
