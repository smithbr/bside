package music

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type Result struct {
	Provider Provider
	Track    Track
	Err      error
}

// Source finds the provider that owns the link.
func Source(providers []Provider, link string) (Provider, *url.URL, error) {
	// Shells escape ? and = when a link is pasted (zsh's url-quote-magic),
	// and inside quotes the backslashes survive. They're never valid in a
	// URL, so drop them.
	u, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(link), `\`, ""))
	if err != nil || u.Host == "" {
		return nil, nil, fmt.Errorf("not a valid link: %q", link)
	}
	for _, p := range providers {
		if p.Owns(u) {
			return p, u, nil
		}
	}
	return nil, nil, fmt.Errorf("unsupported platform: %s", u.Hostname())
}

// FindAll searches every provider except source concurrently, preserving provider order.
func FindAll(ctx context.Context, providers []Provider, source Provider, want Track) []Result {
	var results []Result
	for _, p := range providers {
		if p.ID() != source.ID() {
			results = append(results, Result{Provider: p})
		}
	}
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i].Track, results[i].Err = results[i].Provider.Search(ctx, want)
		})
	}
	wg.Wait()
	return results
}
