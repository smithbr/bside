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
	ErrUnsupported = errors.New("only song and album links are supported")
)

type Kind int

const (
	Song Kind = iota
	Album
)

// Track is a song, or a whole album when Kind is Album. An album's name is
// its Title.
type Track struct {
	Kind       Kind
	Title      string
	Artist     string
	Album      string
	Duration   time.Duration
	TrackCount int
	ISRC       string
	URL        string
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
