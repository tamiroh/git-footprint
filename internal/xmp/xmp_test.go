package xmp

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

const nsDecls = `xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`xmlns:xmp="http://ns.adobe.com/xap/1.0/" ` +
	`xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/" ` +
	`xmlns:mwg-rs="http://www.metadataworkinggroup.com/schemas/regions/" ` +
	`xmlns:MP="http://ns.microsoft.com/photo/1.2/" ` +
	`xmlns:MPRI="http://ns.microsoft.com/photo/1.2/t/RegionInfo#" ` +
	`xmlns:MPReg="http://ns.microsoft.com/photo/1.2/t/Region#"`

// packet builds one <x:xmpmeta> with a single rdf:Description: attrs go on the
// Description element (RDF's attribute form), children go inside it (element
// form). XMP tools use both, sometimes in the same file.
func packet(attrs, children string) string {
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/">` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description ` + nsDecls + ` ` + attrs + `>` + children +
		`</rdf:Description></rdf:RDF></x:xmpmeta>`
}

func TestRead(t *testing.T) {
	tests := map[string]struct {
		in   string
		want Data
	}{
		"attribute form": {
			in: packet(`xmp:CreatorTool="Photoshop 25.0" xmp:CreateDate="2023-03-18T21:40:00+09:00"`,
				`<dc:creator><rdf:Seq><rdf:li>Jane Doe</rdf:li></rdf:Seq></dc:creator>`),
			want: Data{Creator: "Jane Doe", Tool: "Photoshop 25.0", Date: "2023-03-18 21:40:00"},
		},
		"element form": {
			in: packet("", `<xmp:CreatorTool>Lightroom</xmp:CreatorTool>`+
				`<photoshop:DateCreated>2020-06-01</photoshop:DateCreated>`),
			want: Data{Tool: "Lightroom", Date: "2020-06-01 00:00:00"},
		},
		"CreatorTool trailing space trimmed": {
			in:   packet(`xmp:CreatorTool="Pixelmator 3.9   "`, ``),
			want: Data{Tool: "Pixelmator 3.9"},
		},
		"mwg-rs face regions": {
			in: packet("", `<mwg-rs:Regions rdf:parseType="Resource"><mwg-rs:RegionList><rdf:Bag>`+
				`<rdf:li rdf:parseType="Resource"><mwg-rs:Name>Alice</mwg-rs:Name><mwg-rs:Type>Face</mwg-rs:Type></rdf:li>`+
				`<rdf:li rdf:parseType="Resource"><mwg-rs:Name>Bob</mwg-rs:Name></rdf:li>`+
				`</rdf:Bag></mwg-rs:RegionList></mwg-rs:Regions>`),
			want: Data{People: []string{"Alice", "Bob"}},
		},
		"microsoft face regions": {
			in: packet("", `<MP:RegionInfo rdf:parseType="Resource"><MPRI:Regions><rdf:Bag>`+
				`<rdf:li rdf:parseType="Resource"><MPReg:PersonDisplayName>Carol</MPReg:PersonDisplayName></rdf:li>`+
				`</rdf:Bag></MPRI:Regions></MP:RegionInfo>`),
			want: Data{People: []string{"Carol"}},
		},
		"duplicate face name collapsed": {
			in: packet("", `<mwg-rs:Regions rdf:parseType="Resource"><mwg-rs:RegionList><rdf:Bag>`+
				`<rdf:li rdf:parseType="Resource"><mwg-rs:Name>Alice</mwg-rs:Name></rdf:li>`+
				`<rdf:li rdf:parseType="Resource"><mwg-rs:Name>Alice</mwg-rs:Name></rdf:li>`+
				`</rdf:Bag></mwg-rs:RegionList></mwg-rs:Regions>`),
			want: Data{People: []string{"Alice"}},
		},
		"no packet": {
			in:   "just some bytes, no xmp here",
			want: Data{},
		},
		"unterminated packet": {
			in:   "<x:xmpmeta> and then nothing closes it",
			want: Data{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, Read([]byte(tc.in))); diff != "" {
				t.Errorf("Read() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAll(t *testing.T) {
	// a PDF keeps one packet per incremental save; an old author survives here
	blob := "%PDF-1.4 ... objects ... " +
		packet("", `<dc:creator><rdf:Seq><rdf:li>Old Author</rdf:li></rdf:Seq></dc:creator>`) +
		" ... incremental update ... " +
		packet("", `<dc:creator><rdf:Seq><rdf:li>New Author</rdf:li></rdf:Seq></dc:creator>`)

	want := []Data{{Creator: "Old Author"}, {Creator: "New Author"}}
	if diff := cmp.Diff(want, All([]byte(blob))); diff != "" {
		t.Errorf("All() mismatch (-want +got):\n%s", diff)
	}

	if got := All([]byte("no packets at all")); got != nil {
		t.Errorf("All() on empty blob = %v, want nil", got)
	}
}

func TestNormDate(t *testing.T) {
	tests := map[string]string{
		"2023-03-18T21:40:00+09:00": "2023-03-18 21:40:00",
		"2023-03-18T21:40:00":       "2023-03-18 21:40:00",
		"2023-03-18":                "2023-03-18 00:00:00",
		"":                          "",
		"not a date":                "",
		"1850-01-01":                "", // before Plausible's window
	}
	for in, want := range tests {
		if got := normDate(in); got != want {
			t.Errorf("normDate(%q) = %q, want %q", in, got, want)
		}
	}
}
