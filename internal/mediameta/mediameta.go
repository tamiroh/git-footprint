// Package mediameta holds the value helpers the image, video and pdf rules
// share: trimming raw strings, sanity-checking dates, and naming a camera.
package mediameta

import (
	"strings"
	"time"
)

// Clean trims the trailing NULs and spaces that pad fixed-width metadata fields.
func Clean(s string) string { return strings.TrimRight(s, "\x00 ") }

// Plausible rejects dates outside the era of digital media, which a corrupt or
// crafted timestamp field otherwise renders literally.
func Plausible(t time.Time) bool {
	return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1
}

// CameraName joins an EXIF/QuickTime make and model without repeating the maker
// when the model already carries it ("NIKON" + "NIKON D2H" -> "NIKON D2H").
func CameraName(mk, model string) string {
	switch {
	case mk == "":
		return model
	case model == "":
		return mk
	}
	if brand := strings.Fields(mk); len(brand) > 0 &&
		strings.HasPrefix(strings.ToLower(model), strings.ToLower(brand[0])) {
		return model
	}
	return mk + " " + model
}
