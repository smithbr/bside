package music

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSource(t *testing.T) {
	providers := Providers()
	tests := map[string]string{
		"https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=x":         "spotify",
		"https://open.spotify.com/intl-de/track/4cOdK2wGLETKBW3PvgPWqT":      "spotify",
		"https://music.apple.com/us/album/bimbambau/1895056025?i=6762879197": "apple",
		"https://music.youtube.com/watch?v=9fTqkH54Jw8&si=Ojqbg_BxNDrSgJMB":  "youtube",
		"https://youtu.be/9fTqkH54Jw8":                                       "youtube",
		"  https://www.youtube.com/watch?v=9fTqkH54Jw8  ":                    "youtube",
	}
	for link, want := range tests {
		p, _, err := Source(providers, link)
		if err != nil {
			t.Errorf("Source(%q): %v", link, err)
			continue
		}
		if p.ID() != want {
			t.Errorf("Source(%q) = %s, want %s", link, p.ID(), want)
		}
	}
	// A link pasted into zsh inside quotes arrives with ? and = escaped.
	p, u, err := Source(providers, `https://music.youtube.com/watch\?v\=Kf9jrscvBk8`)
	if err != nil || p.ID() != "youtube" || u.Query().Get("v") != "Kf9jrscvBk8" {
		t.Errorf("shell-escaped link: got %v, %v, %v", p, u, err)
	}
	for _, link := range []string{"https://tidal.com/track/1", "not a link"} {
		if _, _, err := Source(providers, link); err == nil {
			t.Errorf("Source(%q) succeeded, want error", link)
		}
	}
}

func TestTrackIDs(t *testing.T) {
	if id, _ := spotifyTrackID(mustURL(t, "https://open.spotify.com/intl-de/track/abc123?si=x")); id != "abc123" {
		t.Errorf("spotify id = %q", id)
	}
	if _, err := spotifyTrackID(mustURL(t, "https://open.spotify.com/album/abc123")); err != ErrUnsupported {
		t.Errorf("spotify album err = %v", err)
	}

	id, country, _ := appleTrackID(mustURL(t, "https://music.apple.com/gb/album/x/1895056025?i=6762879197"))
	if id != "6762879197" || country != "gb" {
		t.Errorf("apple album link = %q, %q", id, country)
	}
	if id, _, _ := appleTrackID(mustURL(t, "https://music.apple.com/us/song/bimbambau/6762879197")); id != "6762879197" {
		t.Errorf("apple song link = %q", id)
	}
	if _, _, err := appleTrackID(mustURL(t, "https://music.apple.com/us/album/bimbambau/1895056025")); err != ErrUnsupported {
		t.Errorf("apple album err = %v", err)
	}

	for _, link := range []string{"https://youtu.be/9fTqkH54Jw8?si=x", "https://music.youtube.com/watch?v=9fTqkH54Jw8"} {
		if id, _ := ytVideoID(mustURL(t, link)); id != "9fTqkH54Jw8" {
			t.Errorf("yt id for %q = %q", link, id)
		}
	}
}

func TestCleanAppleURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://music.apple.com/us/album/x/1?i=2&uo=4":      "https://music.apple.com/us/album/x/1?i=2",
		"https://music.apple.com/us/song/x/1\x1b]52;c;x\x07": "",
	} {
		if got := cleanAppleURL(in); got != want {
			t.Errorf("cleanAppleURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAppleTracksSkipsBadURLs(t *testing.T) {
	tracks := appleTracks([]itunesResult{
		{WrapperType: "track", TrackName: "Bad", TrackViewURL: "https://music.apple.com/us/song/x/1\x1b[2J"},
		{WrapperType: "artist", ArtistName: "Someone"},
		{WrapperType: "track", TrackName: "Good", TrackViewURL: "https://music.apple.com/us/song/x/2"},
	})
	if len(tracks) != 1 || tracks[0].Title != "Good" {
		t.Errorf("appleTracks = %+v, want only Good", tracks)
	}
}

func TestSpotifyTrackURLEscapesID(t *testing.T) {
	got := spotifyTrack{ID: "abc\x1b[2J/../x"}.track().URL
	if want := "https://open.spotify.com/track/abc%1B%5B2J%2F..%2Fx"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBestMatch(t *testing.T) {
	want := Track{Title: "Never Gonna Give You Up", Artist: "Rick Astley", Duration: 213 * time.Second}
	candidates := []Track{
		{Title: "Never Gonna Give You Up (Cover)", Artist: "Some Band", URL: "cover"},
		{Title: "Never Gonna Give You Up - 2022 Remaster", Artist: "Rick Astley", Duration: 214 * time.Second, URL: "remaster"},
		{Title: "Together Forever", Artist: "Rick Astley", URL: "other"},
	}
	got, err := bestMatch(want, candidates)
	if err != nil || got.URL != "remaster" {
		t.Errorf("bestMatch = %q, %v", got.URL, err)
	}

	if _, err := bestMatch(want, []Track{{Title: "Bibimbap", Artist: "TOKiMONSTA"}}); err != ErrNotFound {
		t.Errorf("unrelated candidate err = %v", err)
	}

	isrc := Track{Title: "x", ISRC: "GBARL9300135"}
	got, _ = bestMatch(isrc, []Track{{Title: "x", URL: "a"}, {Title: "different", ISRC: "gbarl9300135", URL: "b"}})
	if got.URL != "b" {
		t.Errorf("ISRC match = %q", got.URL)
	}
}

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"BIMBAMBAU":                         "bimbambau",
		"Duelen (feat. Lido Pimienta)":      "duelen",
		"Beyoncé":                           "beyonce",
		"Simon & Garfunkel":                 "simon and garfunkel",
		"Song Title (Official Music Video)": "song title",
		"Track - Remastered 2011":           "track",
	}
	for in, want := range tests {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"Never Gonna Give You Up":           "Never Gonna Give You Up",
		"Beyoncé — Halo ♫":                  "Beyoncé — Halo ♫",
		"Song\x1b]52;c;ZXZpbA==\x07\x1b[2J": "Song]52;c;ZXZpbA==[2J",
		"Line\nbreak\ttab\r\u009b31m":       "Linebreaktab31m",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseYTSearch(t *testing.T) {
	raw := []byte(`{"contents":{"x":[{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Never Gonna Give You Up"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[
				{"text":"Rick Astley","navigationEndpoint":{"browseEndpoint":{"browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ARTIST"}}}}},
				{"text":" • "},
				{"text":"Whenever You Need Somebody","navigationEndpoint":{"browseEndpoint":{"browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ALBUM"}}}}},
				{"text":" • "},
				{"text":"3:34"}]}}}],
		"playlistItemData":{"videoId":"lYBUbBu4W08"}}}]}}]}}`)
	tracks, err := parseYTSearch(raw)
	if err != nil || len(tracks) != 1 {
		t.Fatalf("parseYTSearch = %v, %v", tracks, err)
	}
	want := Track{
		Title:    "Never Gonna Give You Up",
		Artist:   "Rick Astley",
		Album:    "Whenever You Need Somebody",
		Duration: 214 * time.Second,
		URL:      "https://music.youtube.com/watch?v=lYBUbBu4W08",
	}
	if tracks[0] != want {
		t.Errorf("got %+v, want %+v", tracks[0], want)
	}
}

func TestNewSpotifyCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")

	if s := NewSpotify(); s.clientID != "" || s.clientSecret != "" {
		t.Fatalf("no credentials: got %q/%q", s.clientID, s.clientSecret)
	}

	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"spotify":{"client_id":"file-id","client_secret":"file-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := NewSpotify(); s.clientID != "file-id" || s.clientSecret != "file-secret" {
		t.Fatalf("saved file: got %q/%q", s.clientID, s.clientSecret)
	}

	t.Setenv("SPOTIFY_CLIENT_ID", "env-id")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "env-secret")
	if s := NewSpotify(); s.clientID != "env-id" || s.clientSecret != "env-secret" {
		t.Fatalf("env override: got %q/%q", s.clientID, s.clientSecret)
	}
}

// Earlier versions saved unnested credentials in spotify.json; the first run
// converts them into bside.json and removes the old file.
func TestConfigMigrate(t *testing.T) {
	for _, legacy := range []string{"config", "user config"} {
		t.Run(legacy, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("SPOTIFY_CLIENT_ID", "")
			t.Setenv("SPOTIFY_CLIENT_SECRET", "")

			path, _ := configPath()
			if path != filepath.Join(home, ".config", "bside", "bside.json") {
				t.Fatalf("path = %s, want ~/.config/bside/bside.json", path)
			}
			old := filepath.Join(home, ".config", "bside", "spotify.json")
			if legacy == "user config" {
				dir, _ := os.UserConfigDir()
				if old = filepath.Join(dir, "bside", "spotify.json"); filepath.Dir(old) == filepath.Dir(path) {
					t.Skip("the user config directory is ~/.config on this platform")
				}
			}
			if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte(`{"client_id":"old-id","client_secret":"old-secret"}`), 0o600); err != nil {
				t.Fatal(err)
			}

			if s := NewSpotify(); s.clientID != "old-id" || s.clientSecret != "old-secret" {
				t.Fatalf("legacy file: got %q/%q", s.clientID, s.clientSecret)
			}
			b, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(b), `"spotify"`) {
				t.Errorf("bside.json not written with a spotify section: %s, %v", b, err)
			}
			if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("legacy file still at %s", old)
			}
		})
	}
}

func TestLoadConfigTightensPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(home, ".config", "bside")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o755)
	path := filepath.Join(dir, "bside.json")
	if err := os.WriteFile(path, []byte(`{"spotify":{"client_id":"id","client_secret":"secret"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o644)

	if c := loadConfig(); c.Spotify.ClientSecret != "secret" {
		t.Fatalf("loadConfig = %+v", c)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxBody+1))
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := fetch(req); err == nil {
		t.Fatal("fetch accepted a body over maxBody")
	}
}
