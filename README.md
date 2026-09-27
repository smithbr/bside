# bside

Turn a song link from one streaming platform into links for the others. Supports Spotify, Apple Music, and YouTube Music.

```
$ bside "https://music.apple.com/us/album/bimbambau/1895056025?i=6762879197"

  ╭─────────────────────────────────────────────────────────╮
  │  ▇           BIMBAMBAU                                  │
  │  █ ▆   ▄ ▂   Cain Culto                                 │
  │  █ █ █ █ █   BIMBAMBAU - Single · 2:07 · via Apple Music│
  ╰─────────────────────────────────────────────────────────╯

    ▎ Spotify         ⠧ searching…
    ▎ Apple Music     music.apple.com/us/album/bimbambau/1895056025?i=6762879197  ◆ original
  › ┃ YouTube Music   music.youtube.com/watch?v=Kf9jrscvBk8  ✓ copied

  ↑/k up  ·  ↓/j down  ·  enter copy  ·  o open  ·  q quit
  copied YouTube Music link   ♫
```

## Install

```bash
go install github.com/smithbr/bside@latest
```

## Usage

```
bside [-to spotify|apple|youtube] [-all] <link>
```

- No flags in a terminal: a live list of every platform's link, filling in as each search finishes. `enter` copies the selected link, `o` opens it, `q` quits (the list stays in your scrollback).
- `-to <platform>`: print only that link (good for scripts, e.g. `bside -to spotify "$url" | pbcopy`).
- `-all`, or when output is piped: print every link found as plain text.

## Spotify credentials

Reading Spotify links works without credentials. Searching Spotify requires a free app from the [Spotify developer dashboard](https://developer.spotify.com/dashboard), exposed as `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET`. With 1Password:

```bash
op run --env-file=.env -- bside "$url"
```

where `.env` holds `op://` references rather than secrets.

## How it works

Each platform in `internal/music` implements `Lookup` (read a link into title/artist/duration/ISRC) and `Search` (find the best match in its catalog). Matches are scored by ISRC when both sides have one, otherwise by normalized title and artist similarity with a duration check.

- **Apple Music**: public iTunes Search/Lookup API. New releases missing from the search index are found via the artist's song list.
- **Spotify**: Web API (client credentials); falls back to the public embed page for lookups.
- **YouTube Music**: the unofficial innertube API used by music.youtube.com. If it breaks, bump `ytClientVersion` in `internal/music/ytmusic.go` first.
