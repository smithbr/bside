package music

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

var httpClient = &http.Client{Timeout: 15 * time.Second}

// maxBody caps how much of a response is read. Real responses are well under
// 1 MB; YouTube Music's endpoints are unofficial, so don't trust them to be sane.
const maxBody = 10 << 20

func newRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func fetch(req *http.Request) ([]byte, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Read one byte past the cap so an oversized body is an error rather than
	// silently truncated JSON.
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("%s %s: response larger than %d MB", req.Method, req.URL.Host, maxBody>>20)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s", req.Method, req.URL.Host, resp.Status)
	}
	return b, nil
}

func fetchJSON(req *http.Request, v any) error {
	b, err := fetch(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
