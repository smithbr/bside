package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ErrSpotifyCredentials = errors.New("run `bs setup` to search Spotify")

var nextDataRe = regexp.MustCompile(`<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)

type Spotify struct {
	clientID     string
	clientSecret string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewSpotify uses SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET when both are
// set, otherwise the credentials saved by SetupSpotify.
func NewSpotify() *Spotify {
	c := spotifyCredentials{
		ClientID:     os.Getenv("SPOTIFY_CLIENT_ID"),
		ClientSecret: os.Getenv("SPOTIFY_CLIENT_SECRET"),
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		c = loadConfig().Spotify
	}
	return &Spotify{clientID: c.ClientID, clientSecret: c.ClientSecret}
}

// SetupSpotify checks the credentials with Spotify, then saves them in the
// config file for later runs. It returns the file they were saved to.
func SetupSpotify(ctx context.Context, clientID, clientSecret string) (string, error) {
	s := &Spotify{clientID: clientID, clientSecret: clientSecret}
	if _, err := s.accessToken(ctx); err != nil {
		return "", err
	}
	path, err := configPath()
	if err != nil {
		return "", err
	}
	c := loadConfig()
	c.Spotify = spotifyCredentials{clientID, clientSecret}
	if err := saveConfig(path, c); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Spotify) ID() string   { return "spotify" }
func (s *Spotify) Name() string { return "Spotify" }

func (s *Spotify) Owns(u *url.URL) bool {
	return u.Hostname() == "open.spotify.com"
}

func spotifyTrackID(u *url.URL) (string, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "track" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", ErrUnsupported
}

func (s *Spotify) Lookup(ctx context.Context, u *url.URL) (Track, error) {
	id, err := spotifyTrackID(u)
	if err != nil {
		return Track{}, err
	}
	if s.clientID == "" || s.clientSecret == "" {
		return s.lookupEmbed(ctx, id)
	}
	var t spotifyTrack
	if err := s.api(ctx, "/v1/tracks/"+url.PathEscape(id), nil, &t); err != nil {
		return Track{}, err
	}
	return t.track(), nil
}

// lookupEmbed reads the public embed page, which needs no credentials.
func (s *Spotify) lookupEmbed(ctx context.Context, id string) (Track, error) {
	req, err := newRequest(ctx, http.MethodGet, "https://open.spotify.com/embed/track/"+url.PathEscape(id), nil)
	if err != nil {
		return Track{}, err
	}
	b, err := fetch(req)
	if err != nil {
		return Track{}, err
	}
	m := nextDataRe.FindSubmatch(b)
	if m == nil {
		return Track{}, errors.New("spotify: unexpected embed page format")
	}
	var data struct {
		Props struct {
			PageProps struct {
				State struct {
					Data struct {
						Entity struct {
							Name     string `json:"name"`
							Duration int    `json:"duration"`
							Artists  []struct {
								Name string `json:"name"`
							} `json:"artists"`
						} `json:"entity"`
					} `json:"data"`
				} `json:"state"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &data); err != nil {
		return Track{}, fmt.Errorf("spotify: %w", err)
	}
	e := data.Props.PageProps.State.Data.Entity
	if e.Name == "" {
		return Track{}, ErrNotFound
	}
	t := Track{
		Title:    cleanText(e.Name),
		Duration: time.Duration(e.Duration) * time.Millisecond,
		URL:      "https://open.spotify.com/track/" + url.PathEscape(id),
	}
	if len(e.Artists) > 0 {
		t.Artist = cleanText(e.Artists[0].Name)
	}
	return t, nil
}

func (s *Spotify) Search(ctx context.Context, want Track) (Track, error) {
	if s.clientID == "" || s.clientSecret == "" {
		return Track{}, ErrSpotifyCredentials
	}
	queries := []string{fmt.Sprintf("track:%s artist:%s", want.Title, want.Artist)}
	if want.ISRC != "" {
		queries = append([]string{"isrc:" + want.ISRC}, queries...)
	}
	for _, q := range queries {
		var res struct {
			Tracks struct {
				Items []spotifyTrack `json:"items"`
			} `json:"tracks"`
		}
		params := url.Values{"q": {q}, "type": {"track"}, "limit": {"10"}}
		if err := s.api(ctx, "/v1/search", params, &res); err != nil {
			return Track{}, err
		}
		candidates := make([]Track, len(res.Tracks.Items))
		for i, it := range res.Tracks.Items {
			candidates[i] = it.track()
		}
		if t, err := bestMatch(want, candidates); err == nil {
			return t, nil
		}
	}
	return Track{}, ErrNotFound
}

type spotifyTrack struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DurationMS int    `json:"duration_ms"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

func (st spotifyTrack) track() Track {
	t := Track{
		Title:    cleanText(st.Name),
		Album:    cleanText(st.Album.Name),
		Duration: time.Duration(st.DurationMS) * time.Millisecond,
		ISRC:     st.ExternalIDs.ISRC,
		URL:      "https://open.spotify.com/track/" + url.PathEscape(st.ID),
	}
	if len(st.Artists) > 0 {
		t.Artist = cleanText(st.Artists[0].Name)
	}
	return t
}

func (s *Spotify) api(ctx context.Context, path string, params url.Values, v any) error {
	token, err := s.accessToken(ctx)
	if err != nil {
		return err
	}
	u := "https://api.spotify.com" + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return fetchJSON(req, v)
}

func (s *Spotify) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expires) {
		return s.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://accounts.spotify.com/api/token", strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.clientID, s.clientSecret)
	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := fetchJSON(req, &res); err != nil {
		return "", fmt.Errorf("spotify auth: %w", err)
	}
	s.token = res.AccessToken
	s.expires = time.Now().Add(time.Duration(res.ExpiresIn)*time.Second - time.Minute)
	return s.token, nil
}
