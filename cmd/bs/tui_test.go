package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/smithbr/bside/internal/music"
)

func TestShortLink(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", "open.spotify.com/track/4uLU6h…"},
		{"https://music.apple.com/us/album/bimbambau/1895056025?i=6762879197", "music.apple.com/…/bimbambau"},
		{"https://music.youtube.com/watch?v=Kf9jrscvBk8", "youtube.com/watch?v=Kf9jrscvBk8"},
		{"https://www.example.com/a/b/song", "example.com/…/song"},
		{"https://example.com/", "example.com"},
	}
	for _, tt := range tests {
		if got := shortLink(tt.in); got != tt.want {
			t.Errorf("shortLink(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if n := len([]rune(shortLink(tt.in))); n > linkWidth {
			t.Errorf("shortLink(%q) is %d chars, over the %d-char column", tt.in, n, linkWidth)
		}
	}
}

type fakeProvider struct{ id, url string }

func (f fakeProvider) ID() string           { return f.id }
func (f fakeProvider) Name() string         { return f.id }
func (f fakeProvider) Owns(u *url.URL) bool { return false }
func (f fakeProvider) Lookup(context.Context, *url.URL) (music.Track, error) {
	return music.Track{Title: "Song", Artist: "Artist", URL: f.url}, nil
}
func (f fakeProvider) Search(context.Context, music.Track) (music.Track, error) {
	return music.Track{URL: f.url}, nil
}

func TestRowAt(t *testing.T) {
	ps := []music.Provider{
		fakeProvider{"spotify", "https://open.spotify.com/track/a"},
		fakeProvider{"apple", "https://music.apple.com/us/album/song/1?i=2"},
		fakeProvider{"youtube", "https://music.youtube.com/watch?v=c"},
	}
	load := func(originY, height int) model {
		var m tea.Model = newModel(context.Background(), ps, ps[1], nil)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: height})
		m, _ = m.Update(tea.CursorPositionMsg{Y: originY})
		m, _ = m.Update(lookupMsg{track: music.Track{Title: "Song", Artist: "Artist", URL: ps[1].(fakeProvider).url}})
		return m.(model)
	}

	// The view starts where the cursor was.
	m := load(5, 40)
	for i := range ps {
		if got := m.rowAt(5 + m.listTop() + i); got != i {
			t.Errorf("row %d: rowAt = %d", i, got)
		}
	}
	if got := m.rowAt(5 + m.listTop() - 1); got != -1 {
		t.Errorf("line above the list: rowAt = %d, want -1", got)
	}
	if got := m.rowAt(5 + m.listTop() + len(ps)); got != -1 {
		t.Errorf("line below the list: rowAt = %d, want -1", got)
	}

	// Launched near the bottom, the view pushes the screen up and ends on the
	// last line.
	m = load(38, 40)
	top := 40 - lipgloss.Height(m.content())
	if got := m.rowAt(top + m.listTop()); got != 0 {
		t.Errorf("scrolled view: rowAt = %d, want 0", got)
	}

	// Without a cursor position report, clicks are ignored.
	m = load(0, 40)
	m.originY = -1
	if got := m.rowAt(m.listTop()); got != -1 {
		t.Errorf("unknown origin: rowAt = %d, want -1", got)
	}
}

// Quitting before the terminal answers the startup queries would leave the
// answers for the shell to print, so quit waits for the cursor position.
func TestQuitWaitsForTerminal(t *testing.T) {
	ps := []music.Provider{fakeProvider{"apple", "https://music.apple.com/us/album/song/1?i=2"}}
	var m tea.Model = newModel(context.Background(), ps, ps[0], nil)

	m, cmd := m.Update(lookupMsg{err: music.ErrUnsupported})
	if _, isQuit := cmd().(tea.QuitMsg); isQuit {
		t.Fatal("quit before the terminal answered")
	}
	if !m.(model).quitting || m.(model).err == nil {
		t.Fatal("lookup error should start quitting with the error kept")
	}
	if _, cmd = m.Update(tea.CursorPositionMsg{}); cmd == nil {
		t.Fatal("no command after the terminal answered")
	} else if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("didn't quit once the terminal answered")
	}
}

type owningProvider struct{ fakeProvider }

func (o owningProvider) Owns(u *url.URL) bool { return strings.Contains(u.Host, o.id) }

// Pasting a new link over the results looks it up instead,
// and results still in flight for the old link are dropped.
func TestPasteReplacesLink(t *testing.T) {
	ps := []music.Provider{
		owningProvider{fakeProvider{"spotify", "https://open.spotify.com/track/a"}},
		owningProvider{fakeProvider{"apple", "https://music.apple.com/us/album/song/1?i=2"}},
	}
	old, _ := url.Parse(ps[0].(owningProvider).url)
	var m tea.Model = newModel(context.Background(), ps, ps[0], old)
	m, _ = m.Update(lookupMsg{track: music.Track{Title: "Old", URL: old.String()}})

	m, cmd := m.Update(tea.PasteMsg{Content: "https://music.apple.com/us/album/new/3?i=4\n"})
	if cmd == nil {
		t.Fatal("paste didn't start a lookup")
	}
	got := m.(model)
	if got.source.ID() != "apple" || got.loaded || got.rows != nil {
		t.Fatalf("paste didn't reset to the new link: source=%s loaded=%v rows=%d", got.source.ID(), got.loaded, len(got.rows))
	}

	// A search from the old lookup arrives late and is ignored.
	m, _ = m.Update(searchMsg{gen: 0, index: 0, track: music.Track{URL: "stale"}})
	if m.(model).rows != nil {
		t.Fatal("stale search result was applied")
	}
	if msg, ok := cmd().(lookupMsg); !ok || msg.gen != got.gen {
		t.Fatalf("lookup for new link: %#v", msg)
	}

	// Pasting junk changes nothing.
	if _, cmd = m.Update(tea.PasteMsg{Content: "hello"}); cmd != nil {
		t.Fatal("junk paste started a lookup")
	}
}

func TestOpenURLRejectsNonHTTPS(t *testing.T) {
	for _, u := range []string{
		"http://music.apple.com/us/song/x/1",
		"file:///Applications/Calculator.app",
		"-a Calculator",
		"https:///no-host",
		"javascript:alert(1)",
	} {
		if err := openURL(u); err == nil {
			t.Errorf("openURL(%q) = nil, want an error", u)
		}
	}
}
