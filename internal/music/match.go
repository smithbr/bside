package music

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const minScore = 0.6

// Bracketed or dashed suffixes that differ between platforms for the same recording,
// e.g. "(feat. X)", "[Remastered 2009]", "- Radio Edit", "(Official Video)".
var noiseRe = regexp.MustCompile(`(?i)\s*[\(\[][^\)\]]*(feat|ft\.|with |remaster|version|edit|official|video|audio|lyric|explicit|clean)[^\)\]]*[\)\]]|\s+-\s+.*(remaster|version|edit|mix).*$`)

var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

func normalize(s string) string {
	s = noiseRe.ReplaceAllString(s, "")
	if t, _, err := transform.String(stripMarks, s); err == nil {
		s = t
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

func similarity(a, b string) float64 {
	a, b = normalize(a), normalize(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.9
	}
	wa, wb := strings.Fields(a), strings.Fields(b)
	seen := make(map[string]bool, len(wa))
	for _, w := range wa {
		seen[w] = true
	}
	shared := 0
	for _, w := range wb {
		if seen[w] {
			shared++
			delete(seen, w)
		}
	}
	return 2 * float64(shared) / float64(len(wa)+len(wb))
}

func score(want, got Track) float64 {
	if want.ISRC != "" && strings.EqualFold(want.ISRC, got.ISRC) {
		return 2
	}
	s := 0.6*similarity(want.Title, got.Title) + 0.4*similarity(want.Artist, got.Artist)
	if want.Duration > 0 && got.Duration > 0 {
		diff := want.Duration - got.Duration
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff <= 3*time.Second:
			s += 0.05
		case diff > 30*time.Second:
			s -= 0.15
		}
	}
	return s
}

func bestMatch(want Track, candidates []Track) (Track, error) {
	var best Track
	bestScore := 0.0
	for _, c := range candidates {
		if s := score(want, c); s > bestScore {
			best, bestScore = c, s
		}
	}
	if bestScore < minScore {
		return Track{}, ErrNotFound
	}
	return best, nil
}
