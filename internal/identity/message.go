package identity

import (
	"fmt"
	"regexp"
	"strings"
)

// Mention records evidence in a commit message, not an authorship claim.
// Name is set only for a trailer whose value is a Name <email> identity.
type Mention struct {
	Commit   string
	Location string
	Name     string
	Email    string
}

// Match common unquoted email addresses, including internal, single-label
// domains. This deliberately does not attempt to identify bare personal names.
var messageEmail = regexp.MustCompile(`[a-zA-Z0-9_][a-zA-Z0-9_.%+'-]*@[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*`)

func messageMentions(commit, message, rawTrailers, trailers, separators string) []Mention {
	var mentions []Mention
	seen := map[Mention]bool{}
	add := func(location, name, email string) {
		m := Mention{Commit: commit, Location: location, Name: name, Email: strings.ToLower(email)}
		if !seen[m] {
			mentions = append(mentions, m)
			seen[m] = true
		}
	}
	scan := func(location, value string) {
		for _, email := range messageEmail.FindAllString(value, -1) {
			local, _, _ := strings.Cut(email, "@")
			if local != "" && !strings.HasSuffix(local, ".") && !strings.Contains(local, "..") {
				add(location, "", email)
			}
		}
	}

	// Git supplies both the original trailer block and its parsed, unfolded
	// entries. Skip parsed entries in the raw message, but keep non-trailer
	// lines that Git may allow within a trailer block.
	keys := map[string]string{}
	for _, line := range strings.Split(trailers, "\n") {
		if key, _, ok := strings.Cut(line, ":"); ok {
			if keys[strings.ToLower(key)] == "" {
				keys[strings.ToLower(key)] = key
			}
		}
	}
	body := strings.TrimRight(message, "\r\n")
	trailerStart, trailerEnd := len(body), len(body)
	if raw := strings.TrimRight(rawTrailers, "\r\n"); raw != "" {
		if start := strings.LastIndex(body, raw); start >= 0 {
			trailerStart, trailerEnd = start, start+len(raw)
		}
	}
	offset, inTrailer := 0, false
	for i, line := range strings.Split(body, "\n") {
		if offset >= trailerStart && offset < trailerEnd {
			if sep := strings.IndexAny(line, separators); sep >= 0 && keys[strings.ToLower(strings.TrimSpace(line[:sep]))] != "" {
				inTrailer = true
			} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				inTrailer = false
			}
		} else {
			inTrailer = false
		}
		offset += len(line) + 1
		if inTrailer {
			continue
		}
		location := fmt.Sprintf("body line %d", i+1)
		if i == 0 {
			location = "subject"
		}
		scan(location, line)
	}
	for _, line := range strings.Split(trailers, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		location := "trailer " + keys[strings.ToLower(key)]
		if name, email, ok := parseCoAuthor(value); ok {
			// Co-authors are already listed as contributors, like authors and committers.
			if strings.EqualFold(key, "Co-Authored-by") {
				continue
			}
			add(location, name, email)
		} else {
			scan(location, value)
		}
	}
	return mentions
}
