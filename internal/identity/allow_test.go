package identity

import (
	"testing"

	"github.com/tamiroh/git-footprint/internal/rule"
)

func TestAllowListFindings(t *testing.T) {
	identities := []Identity{
		{Name: "Example", Email: "public@example.com", Self: IsSelf},
		{Name: "Private Name", Email: "public@example.com", Self: MaybeSelf},
		{Name: "build[bot]", Email: "bot@example.com", Bot: true},
	}
	for _, tc := range []struct {
		name       string
		allowed    []string
		identities []Identity
		want       []Identity
	}{
		{
			name:       "unspecified allow list",
			identities: identities,
		},
		{
			name:       "email matching ignores case",
			allowed:    []string{"Example <PUBLIC@example.com>"},
			identities: identities,
			want:       identities[1:],
		},
		{
			name:       "duplicate entries",
			allowed:    []string{"Example <public@example.com>", "Example <public@example.com>"},
			identities: identities,
			want:       identities[1:],
		},
		{
			name:       "multiple allowed identities",
			allowed:    []string{"Example <public@example.com>", "build[bot] <bot@example.com>"},
			identities: identities,
			want:       identities[1:2],
		},
		{
			name:       "self and bots are not implicitly allowed",
			allowed:    []string{"Other <other@example.com>"},
			identities: identities,
			want:       identities,
		},
		{
			name:    "plain directory",
			allowed: []string{"Example <public@example.com>"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			var a AllowList
			for _, value := range tc.allowed {
				if err := a.Set(value); err != nil {
					t.Fatal(err)
				}
			}
			fp := Footprint{Identities: tc.identities}

			// Act
			got := a.Findings(fp)

			// Assert
			if len(got) != len(tc.want) {
				t.Fatalf("Findings() = %v, want warnings for %v", got, tc.want)
			}
			for i, id := range tc.want {
				wantAuthor := rule.Author{Name: id.Name, Email: id.Email}
				if got[i].By != wantAuthor || got[i].Level() != rule.Warn {
					t.Errorf("Findings()[%d] = %v, want warning for %v", i, got[i], wantAuthor)
				}
			}
		})
	}
}

func TestAllowListInvalid(t *testing.T) {
	for _, value := range []string{"", "email@example.com", "Name", "<email@example.com>", "Name <>", "Name <a@example.com> extra", "Name < a@example.com>", "Name\nOther <a@example.com>"} {
		t.Run(value, func(t *testing.T) {
			// Arrange
			var a AllowList

			// Act
			err := a.Set(value)

			// Assert
			if err == nil {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}
