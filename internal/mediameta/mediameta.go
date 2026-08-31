// Package mediameta holds the value helpers the image, video and pdf rules share.
package mediameta

import (
	"strings"
	"time"
)

func Clean(s string) string { return strings.TrimRight(s, "\x00 ") }

func FirstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// Plausible rejects timestamps a corrupt or crafted field would render literally.
func Plausible(t time.Time) bool {
	return t.Year() >= 1980 && t.Year() <= time.Now().Year()+1
}

// CameraName: "NIKON" + "NIKON D2H" -> "NIKON D2H"
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
