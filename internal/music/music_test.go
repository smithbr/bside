package music

import (
	"net/url"
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
	got := cleanAppleURL("https://music.apple.com/us/album/x/1?i=2&uo=4")
	if want := "https://music.apple.com/us/album/x/1?i=2"; got != want {
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
