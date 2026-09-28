package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// YouTube Music has no public API; these are the internal "innertube" endpoints
// the music.youtube.com web client uses.
const (
	ytAPI           = "https://music.youtube.com/youtubei/v1/"
	ytClientVersion = "1.20260901.01.00"
	ytSongsFilter   = "EgWKAQIIAWoKEAoQCRADEAQQBQ%3D%3D"
	ytAlbumsFilter  = "EgWKAQIYAWoKEAoQCRADEAQQBQ%3D%3D"
	ytAlbumPage     = "MUSIC_PAGE_TYPE_ALBUM"
	ytArtistPage    = "MUSIC_PAGE_TYPE_ARTIST"
)

var ytDurationRe = regexp.MustCompile(`^(\d+:)?\d{1,2}:\d{2}$`)

type YouTubeMusic struct{}

func NewYouTubeMusic() *YouTubeMusic { return &YouTubeMusic{} }

func (y *YouTubeMusic) ID() string   { return "youtube" }
func (y *YouTubeMusic) Name() string { return "YouTube Music" }

func (y *YouTubeMusic) Owns(u *url.URL) bool {
	switch u.Hostname() {
	case "music.youtube.com", "youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be":
		return true
	}
	return false
}

func ytVideoID(u *url.URL) (string, error) {
	if u.Hostname() == "youtu.be" {
		if id := strings.Trim(u.Path, "/"); id != "" {
			return id, nil
		}
	}
	if id := u.Query().Get("v"); id != "" {
		return id, nil
	}
	return "", ErrUnsupported
}

// ytAlbumID handles album links: /browse/MPREb_<id>, and the album's own
// playlist, /playlist?list=OLAK5uy_<id>. It returns whichever ID the link has.
func ytAlbumID(u *url.URL) (browseID, playlistID string, ok bool) {
	if id, found := strings.CutPrefix(u.Path, "/browse/"); found && strings.HasPrefix(id, "MPREb_") {
		return id, "", true
	}
	if list := u.Query().Get("list"); u.Path == "/playlist" && strings.HasPrefix(list, "OLAK5uy_") {
		return "", list, true
	}
	return "", "", false
}

func ytWatchURL(id string) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(id)
}

func ytAlbumURL(browseID string) string {
	return "https://music.youtube.com/browse/" + url.PathEscape(browseID)
}

func (y *YouTubeMusic) call(ctx context.Context, endpoint string, body map[string]any, v any) error {
	body["context"] = map[string]any{
		"client": map[string]any{
			"clientName":    "WEB_REMIX",
			"clientVersion": ytClientVersion,
			"hl":            "en",
			"gl":            "US",
		},
	}
	req, err := newRequest(ctx, http.MethodPost, ytAPI+endpoint+"?prettyPrint=false", body)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", "https://music.youtube.com")
	return fetchJSON(req, v)
}

func (y *YouTubeMusic) Lookup(ctx context.Context, u *url.URL) (Track, error) {
	if browseID, playlistID, ok := ytAlbumID(u); ok {
		return y.lookupAlbum(ctx, browseID, playlistID)
	}
	id, err := ytVideoID(u)
	if err != nil {
		return Track{}, err
	}
	var res struct {
		VideoDetails struct {
			Title         string `json:"title"`
			Author        string `json:"author"`
			LengthSeconds string `json:"lengthSeconds"`
		} `json:"videoDetails"`
	}
	if err := y.call(ctx, "player", map[string]any{"videoId": id}, &res); err != nil {
		return Track{}, err
	}
	d := res.VideoDetails
	if d.Title == "" {
		return Track{}, ErrNotFound
	}
	secs, _ := strconv.Atoi(d.LengthSeconds)
	artist := cleanText(strings.TrimSuffix(strings.TrimSuffix(d.Author, " - Topic"), "VEVO"))
	title := strings.TrimPrefix(cleanText(d.Title), artist+" - ")
	return Track{
		Title:    title,
		Artist:   artist,
		Duration: time.Duration(secs) * time.Second,
		URL:      ytWatchURL(id),
	}, nil
}

func (y *YouTubeMusic) lookupAlbum(ctx context.Context, browseID, playlistID string) (Track, error) {
	if browseID == "" {
		// The playlist page links each of its songs to the album page.
		var raw json.RawMessage
		if err := y.call(ctx, "browse", map[string]any{"browseId": "VL" + playlistID}, &raw); err != nil {
			return Track{}, err
		}
		var err error
		if browseID, err = firstYTAlbumLink(raw); err != nil {
			return Track{}, err
		}
	}
	var raw json.RawMessage
	if err := y.call(ctx, "browse", map[string]any{"browseId": browseID}, &raw); err != nil {
		return Track{}, err
	}
	t, err := parseYTAlbum(raw)
	if err != nil {
		return Track{}, err
	}
	t.URL = ytAlbumURL(browseID)
	return t, nil
}

func (y *YouTubeMusic) Search(ctx context.Context, want Track) (Track, error) {
	var raw json.RawMessage
	filter := ytSongsFilter
	if want.Kind == Album {
		filter = ytAlbumsFilter
	}
	body := map[string]any{"query": want.Title + " " + want.Artist, "params": filter}
	if err := y.call(ctx, "search", body, &raw); err != nil {
		return Track{}, err
	}
	candidates, err := parseYTSearch(raw)
	if err != nil {
		return Track{}, err
	}
	return bestMatch(want, candidates)
}

type ytNavigation struct {
	BrowseEndpoint struct {
		BrowseID string `json:"browseId"`
		Configs  struct {
			Music struct {
				PageType string `json:"pageType"`
			} `json:"browseEndpointContextMusicConfig"`
		} `json:"browseEndpointContextSupportedConfigs"`
	} `json:"browseEndpoint"`
}

func (n ytNavigation) pageType() string { return n.BrowseEndpoint.Configs.Music.PageType }

type ytRun struct {
	Text               string       `json:"text"`
	NavigationEndpoint ytNavigation `json:"navigationEndpoint"`
}

type ytText struct {
	Runs []ytRun `json:"runs"`
}

func (t ytText) first() string {
	if len(t.Runs) == 0 {
		return ""
	}
	return cleanText(t.Runs[0].Text)
}

type ytListItem struct {
	FlexColumns []struct {
		Renderer struct {
			Text ytText `json:"text"`
		} `json:"musicResponsiveListItemFlexColumnRenderer"`
	} `json:"flexColumns"`
	PlaylistItemData struct {
		VideoID string `json:"videoId"`
	} `json:"playlistItemData"`
	NavigationEndpoint ytNavigation `json:"navigationEndpoint"`
}

// ytFind pulls every value under key out of a deeply nested response rather
// than depending on its exact shape, and decodes each into a T.
func ytFind[T any](raw json.RawMessage, key string) ([]T, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var found []T
	var walk func(any)
	walk = func(o any) {
		switch v := o.(type) {
		case map[string]any:
			for k, child := range v {
				if k != key {
					walk(child)
					continue
				}
				b, _ := json.Marshal(child)
				var t T
				if json.Unmarshal(b, &t) == nil {
					found = append(found, t)
				}
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(root)
	return found, nil
}

// firstYTAlbumLink finds the album page a playlist's songs link to.
func firstYTAlbumLink(raw json.RawMessage) (string, error) {
	navs, err := ytFind[ytNavigation](raw, "navigationEndpoint")
	if err != nil {
		return "", err
	}
	for _, n := range navs {
		if n.pageType() == ytAlbumPage && n.BrowseEndpoint.BrowseID != "" {
			return n.BrowseEndpoint.BrowseID, nil
		}
	}
	return "", ErrNotFound
}

func parseYTAlbum(raw json.RawMessage) (Track, error) {
	headers, err := ytFind[struct {
		Title          ytText `json:"title"`
		SecondSubtitle ytText `json:"secondSubtitle"`
		Strapline      ytText `json:"straplineTextOne"`
	}](raw, "musicResponsiveHeaderRenderer")
	if err != nil {
		return Track{}, err
	}
	if len(headers) == 0 || headers[0].Title.first() == "" {
		return Track{}, ErrNotFound
	}
	h := headers[0]
	t := Track{Kind: Album, Title: h.Title.first(), Artist: h.Strapline.first()}
	// "12 songs"
	if n, _, ok := strings.Cut(h.SecondSubtitle.first(), " "); ok {
		t.TrackCount, _ = strconv.Atoi(n)
	}
	return t, nil
}

// parseYTSearch reads the songs and albums out of a search response.
func parseYTSearch(raw json.RawMessage) ([]Track, error) {
	items, err := ytFind[ytListItem](raw, "musicResponsiveListItemRenderer")
	if err != nil {
		return nil, err
	}
	var tracks []Track
	for _, li := range items {
		if len(li.FlexColumns) < 2 {
			continue
		}
		var t Track
		switch browseID := li.NavigationEndpoint.BrowseEndpoint.BrowseID; {
		case li.PlaylistItemData.VideoID != "":
			t.URL = ytWatchURL(li.PlaylistItemData.VideoID)
		case li.NavigationEndpoint.pageType() == ytAlbumPage && browseID != "":
			t.Kind, t.URL = Album, ytAlbumURL(browseID)
		default:
			continue
		}
		t.Title = li.FlexColumns[0].Renderer.Text.first()
		for _, r := range li.FlexColumns[1].Renderer.Text.Runs {
			switch {
			case r.NavigationEndpoint.pageType() == ytArtistPage && t.Artist == "":
				t.Artist = cleanText(r.Text)
			case r.NavigationEndpoint.pageType() == ytAlbumPage:
				t.Album = cleanText(r.Text)
			case ytDurationRe.MatchString(r.Text):
				t.Duration = parseClock(r.Text)
			}
		}
		tracks = append(tracks, t)
	}
	return tracks, nil
}

func parseClock(s string) time.Duration {
	var total int
	for _, part := range strings.Split(s, ":") {
		n, _ := strconv.Atoi(part)
		total = total*60 + n
	}
	return time.Duration(total) * time.Second
}
