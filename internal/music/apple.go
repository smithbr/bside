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

// appleID handles album links, with ?i=<track> for one of their songs, and
// /song/<slug>/<id> links.
func appleID(u *url.URL) (id, country string, kind Kind, err error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	country = "us"
	if len(parts) > 0 && len(parts[0]) == 2 {
		country = parts[0]
	}
	if i := u.Query().Get("i"); i != "" {
		return i, country, Song, nil
	}
	for i, p := range parts {
		if (p == "song" || p == "album") && i+1 < len(parts) {
			kind = Song
			if p == "album" {
				kind = Album
			}
			return parts[len(parts)-1], country, kind, nil
		}
	}
	return "", "", Song, ErrUnsupported
}

// itunesEntity is the lookup and search entity for a kind.
func itunesEntity(k Kind) string {
	if k == Album {
		return "album"
	}
	return "song"
}

type itunesResult struct {
	WrapperType       string `json:"wrapperType"`
	ArtistID          int    `json:"artistId"`
	TrackName         string `json:"trackName"`
	ArtistName        string `json:"artistName"`
	CollectionName    string `json:"collectionName"`
	CollectionViewURL string `json:"collectionViewUrl"`
	TrackCount        int    `json:"trackCount"`
	TrackTimeMillis   int    `json:"trackTimeMillis"`
	TrackViewURL      string `json:"trackViewUrl"`
}

func (r itunesResult) track() Track {
	return Track{
		Title:    cleanText(r.TrackName),
		Artist:   cleanText(r.ArtistName),
		Album:    cleanText(r.CollectionName),
		Duration: time.Duration(r.TrackTimeMillis) * time.Millisecond,
		URL:      cleanAppleURL(r.TrackViewURL),
	}
}

func (r itunesResult) album() Track {
	return Track{
		Kind:       Album,
		Title:      cleanText(r.CollectionName),
		Artist:     cleanText(r.ArtistName),
		TrackCount: r.TrackCount,
		URL:        cleanAppleURL(r.CollectionViewURL),
	}
}

// cleanAppleURL drops the iTunes affiliate/tracking params, keeping ?i=. It
// returns "" for a link that doesn't parse, such as one with control characters.
func cleanAppleURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
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
	id, country, kind, err := appleID(u)
	if err != nil {
		return Track{}, err
	}
	results, err := a.itunes(ctx, "lookup", url.Values{"id": {id}, "country": {country}, "entity": {itunesEntity(kind)}})
	if err != nil {
		return Track{}, err
	}
	if tracks := appleTracks(results, kind); len(tracks) > 0 {
		return tracks[0], nil
	}
	return Track{}, ErrNotFound
}

func (a *Apple) Search(ctx context.Context, want Track) (Track, error) {
	results, err := a.itunes(ctx, "search", url.Values{
		"term":    {want.Title + " " + want.Artist},
		"entity":  {itunesEntity(want.Kind)},
		"limit":   {"10"},
		"country": {a.country},
	})
	if err != nil {
		return Track{}, err
	}
	if t, err := bestMatch(want, appleTracks(results, want.Kind)); err == nil {
		return t, nil
	}
	return a.searchByArtist(ctx, want)
}

// searchByArtist covers new releases, which show up in lookups well before
// they're added to the search index, and albums the search misses entirely.
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
	found, err := a.itunes(ctx, "lookup", url.Values{
		"id":      {strconv.Itoa(artists[0].ArtistID)},
		"entity":  {itunesEntity(want.Kind)},
		"limit":   {"200"},
		"sort":    {"recent"},
		"country": {a.country},
	})
	if err != nil {
		return Track{}, err
	}
	return bestMatch(want, appleTracks(found, want.Kind))
}

// appleTracks keeps the songs, or the albums, from a mix of results.
func appleTracks(results []itunesResult, kind Kind) []Track {
	var tracks []Track
	for _, r := range results {
		var t Track
		switch {
		case kind == Song && r.WrapperType == "track":
			t = r.track()
		case kind == Album && r.WrapperType == "collection":
			t = r.album()
		default:
			continue
		}
		if t.URL != "" {
			tracks = append(tracks, t)
		}
	}
	return tracks
}
