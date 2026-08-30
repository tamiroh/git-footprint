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
	IsYou            bool
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

// Build assembles the identity footprint, sorted with "you" first and then by
// total commits. "You" is the identity whose email exactly matches
// git config user.email (or, if that is unset, whose name matches user.name).
func Build(repo string) (Footprint, error) {
	youEmail := strings.ToLower(gitcmd.Try(repo, "config", "user.email"))
	youName := gitcmd.Try(repo, "config", "user.name")

	ids, err := collect(repo)
	if err != nil {
		return Footprint{}, err
	}

	switch {
	case youEmail != "":
		for i := range ids {
			ids[i].IsYou = ids[i].Email == youEmail
		}
	case youName != "":
		for i := range ids {
			ids[i].IsYou = ids[i].Name == youName
		}
	}

	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i].IsYou != ids[j].IsYou {
			return ids[i].IsYou
		}
		return ids[i].AuthorCommits+ids[i].CommitterCommits >
			ids[j].AuthorCommits+ids[j].CommitterCommits
	})

	commits := 0
	for _, id := range ids {
		commits += id.AuthorCommits
	}
	return Footprint{TotalCommits: commits, Identities: ids}, nil
}
