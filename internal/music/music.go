package music

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotFound    = errors.New("no match found")
	ErrUnsupported = errors.New("only song links are supported")
)

type Track struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	ISRC     string
	URL      string
}

// Provider is one streaming platform: it can read its own links and search its catalog.
type Provider interface {
	ID() string
	Name() string
	Owns(u *url.URL) bool
	Lookup(ctx context.Context, u *url.URL) (Track, error)
	Search(ctx context.Context, want Track) (Track, error)
}

func Providers() []Provider {
	return []Provider{NewSpotify(), NewApple(), NewYouTubeMusic()}
}

// cleanText drops control characters so a title or artist name someone else
// wrote can't smuggle terminal escape sequences onto the screen.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
