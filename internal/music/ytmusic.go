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

func ytWatchURL(id string) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(id)
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

func (y *YouTubeMusic) Search(ctx context.Context, want Track) (Track, error) {
	var raw json.RawMessage
	body := map[string]any{"query": want.Title + " " + want.Artist, "params": ytSongsFilter}
	if err := y.call(ctx, "search", body, &raw); err != nil {
		return Track{}, err
	}
	candidates, err := parseYTSearch(raw)
	if err != nil {
		return Track{}, err
	}
	return bestMatch(want, candidates)
}

type ytRun struct {
	Text               string `json:"text"`
	NavigationEndpoint struct {
		BrowseEndpoint struct {
			Configs struct {
				Music struct {
					PageType string `json:"pageType"`
				} `json:"browseEndpointContextMusicConfig"`
			} `json:"browseEndpointContextSupportedConfigs"`
		} `json:"browseEndpoint"`
	} `json:"navigationEndpoint"`
}

type ytListItem struct {
	FlexColumns []struct {
		Renderer struct {
			Text struct {
				Runs []ytRun `json:"runs"`
			} `json:"text"`
		} `json:"musicResponsiveListItemFlexColumnRenderer"`
	} `json:"flexColumns"`
	PlaylistItemData struct {
		VideoID string `json:"videoId"`
	} `json:"playlistItemData"`
}

// parseYTSearch pulls every musicResponsiveListItemRenderer out of the deeply
// nested search response rather than depending on its exact shape.
func parseYTSearch(raw json.RawMessage) ([]Track, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var items []any
	var walk func(any)
	walk = func(o any) {
		switch v := o.(type) {
		case map[string]any:
			if item, ok := v["musicResponsiveListItemRenderer"]; ok {
				items = append(items, item)
				return
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(root)

	var tracks []Track
	for _, it := range items {
		b, _ := json.Marshal(it)
		var li ytListItem
		if err := json.Unmarshal(b, &li); err != nil || li.PlaylistItemData.VideoID == "" || len(li.FlexColumns) < 2 {
			continue
		}
		t := Track{URL: ytWatchURL(li.PlaylistItemData.VideoID)}
		if runs := li.FlexColumns[0].Renderer.Text.Runs; len(runs) > 0 {
			t.Title = cleanText(runs[0].Text)
		}
		for _, r := range li.FlexColumns[1].Renderer.Text.Runs {
			switch {
			case r.NavigationEndpoint.BrowseEndpoint.Configs.Music.PageType == "MUSIC_PAGE_TYPE_ARTIST" && t.Artist == "":
				t.Artist = cleanText(r.Text)
			case r.NavigationEndpoint.BrowseEndpoint.Configs.Music.PageType == "MUSIC_PAGE_TYPE_ALBUM":
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
