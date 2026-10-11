package identity

import (
	"fmt"
	"strings"

	"github.com/tamiroh/git-footprint/internal/rule"
)

// AllowList matches names exactly and emails case-insensitively, like collect.
// Its zero value disables identity warnings. It implements flag.Value.
type AllowList struct {
	entries []rule.Author
}

func (a *AllowList) String() string {
	var values []string
	for _, id := range a.entries {
		values = append(values, id.Name+" <"+id.Email+">")
	}
	return strings.Join(values, ", ")
}

func (a *AllowList) Set(value string) error {
	value = strings.TrimSpace(value)
	i := strings.LastIndexByte(value, '<')
	if i < 1 || !strings.HasSuffix(value, ">") {
		return fmt.Errorf("want Name <email>")
	}
	name, email := strings.TrimSpace(value[:i]), value[i+1:len(value)-1]
	if name == "" || email == "" || strings.ContainsAny(name+email, "<>\r\n\x00") || strings.TrimSpace(email) != email {
		return fmt.Errorf("want Name <email>")
	}
	a.entries = append(a.entries, rule.Author{Name: name, Email: strings.ToLower(email)})
	return nil
}

func (a *AllowList) Findings(fp Footprint) []rule.Finding {
	if len(a.entries) == 0 {
		return nil
	}
	allowed := make(map[rule.Author]bool, len(a.entries))
	for _, id := range a.entries {
		allowed[id] = true
	}
	var findings []rule.Finding
	for _, id := range fp.Identities {
		if allowed[rule.Author{Name: id.Name, Email: strings.ToLower(id.Email)}] {
			continue
		}
		findings = append(findings, rule.Finding{
			Detector: "identity",
			By:       rule.Author{Name: id.Name, Email: id.Email},
			Checks:   []rule.Check{{Name: "identity", Level: rule.Warn, Value: "identity is not in the allow list"}},
		})
	}
	return findings
}
