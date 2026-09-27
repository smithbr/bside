package music

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Apple struct {
	country string
}

func NewApple() *Apple {
	return &Apple{country: "us"}
}

func (a *Apple) ID() string   { return "apple" }
func (a *Apple) Name() string { return "Apple Music" }

func (a *Apple) Owns(u *url.URL) bool {
	switch u.Hostname() {
	case "music.apple.com", "geo.music.apple.com", "itunes.apple.com":
		return true
	}
	return false
}

// appleTrackID handles album links with ?i=<track> and /song/<slug>/<id> links.
func appleTrackID(u *url.URL) (id, country string, err error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	country = "us"
	if len(parts) > 0 && len(parts[0]) == 2 {
		country = parts[0]
	}
	if i := u.Query().Get("i"); i != "" {
		return i, country, nil
	}
	for i, p := range parts {
		if p == "song" && i+1 < len(parts) {
			return parts[len(parts)-1], country, nil
		}
	}
	return "", "", ErrUnsupported
}

type itunesResult struct {
	WrapperType     string `json:"wrapperType"`
	ArtistID        int    `json:"artistId"`
	TrackName       string `json:"trackName"`
	ArtistName      string `json:"artistName"`
	CollectionName  string `json:"collectionName"`
	TrackTimeMillis int    `json:"trackTimeMillis"`
	TrackViewURL    string `json:"trackViewUrl"`
}

func (r itunesResult) track() Track {
	return Track{
		Title:    r.TrackName,
		Artist:   r.ArtistName,
		Album:    r.CollectionName,
		Duration: time.Duration(r.TrackTimeMillis) * time.Millisecond,
		URL:      cleanAppleURL(r.TrackViewURL),
	}
}

// cleanAppleURL drops the iTunes affiliate/tracking params, keeping ?i=.
func cleanAppleURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := url.Values{}
	if i := u.Query().Get("i"); i != "" {
		q.Set("i", i)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *Apple) itunes(ctx context.Context, endpoint string, params url.Values) ([]itunesResult, error) {
	req, err := newRequest(ctx, http.MethodGet, "https://itunes.apple.com/"+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Results []itunesResult `json:"results"`
	}
	if err := fetchJSON(req, &res); err != nil {
		return nil, err
	}
	return res.Results, nil
}

func (a *Apple) Lookup(ctx context.Context, u *url.URL) (Track, error) {
	id, country, err := appleTrackID(u)
	if err != nil {
		return Track{}, err
	}
	results, err := a.itunes(ctx, "lookup", url.Values{"id": {id}, "country": {country}, "entity": {"song"}})
	if err != nil {
		return Track{}, err
	}
	for _, r := range results {
		if r.WrapperType == "track" {
			return r.track(), nil
		}
	}
	return Track{}, ErrNotFound
}

func (a *Apple) Search(ctx context.Context, want Track) (Track, error) {
	results, err := a.itunes(ctx, "search", url.Values{
		"term":    {want.Title + " " + want.Artist},
		"entity":  {"song"},
		"limit":   {"10"},
		"country": {a.country},
	})
	if err != nil {
		return Track{}, err
	}
	if t, err := bestMatch(want, appleTracks(results)); err == nil {
		return t, nil
	}
	return a.searchByArtist(ctx, want)
}

// searchByArtist covers new releases, which show up in lookups well before
// they're added to the song search index.
func (a *Apple) searchByArtist(ctx context.Context, want Track) (Track, error) {
	artists, err := a.itunes(ctx, "search", url.Values{
		"term":    {want.Artist},
		"entity":  {"musicArtist"},
		"limit":   {"1"},
		"country": {a.country},
	})
	if err != nil || len(artists) == 0 {
		return Track{}, ErrNotFound
	}
	songs, err := a.itunes(ctx, "lookup", url.Values{
		"id":      {strconv.Itoa(artists[0].ArtistID)},
		"entity":  {"song"},
		"limit":   {"200"},
		"sort":    {"recent"},
		"country": {a.country},
	})
	if err != nil {
		return Track{}, err
	}
	return bestMatch(want, appleTracks(songs))
}

func appleTracks(results []itunesResult) []Track {
	var tracks []Track
	for _, r := range results {
		if r.WrapperType == "track" {
			tracks = append(tracks, r.track())
		}
	}
	return tracks
}
