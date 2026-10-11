package identity

import "testing"

func TestParseTrailer(t *testing.T) {
	for _, tc := range []struct {
		line, key, name, email string
		ok                     bool
	}{
		{"Co-authored-by: Ada Lovelace <ada@example.com>", "Co-authored-by", "Ada Lovelace", "ada@example.com", true},
		{"SIGNED-OFF-BY:  Ada <Ada@Example.com> ", "Signed-off-by", "Ada", "Ada@Example.com", true},
		{"Reviewed-by: A <B> C <c@example.com>", "Reviewed-by", "A <B> C", "c@example.com", true},
		{"Link: see <https://example.com/a@b>", "", "", "", false},
		{"Fixes: #12", "", "", "", false},
		{"Reported-by: <anon@example.com>", "", "", "", false},
		{"Co-authored-by: Ada <not an email>", "", "", "", false},
		{"", "", "", "", false},
	} {
		key, name, email, ok := parseTrailer(tc.line)
		if ok != tc.ok || key != tc.key || name != tc.name || email != tc.email {
			t.Errorf("parseTrailer(%q) = %q, %q, %q, %v", tc.line, key, name, email, ok)
		}
	}
}
