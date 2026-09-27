# bside

Turn a song link into links for the other streaming apps. Supports Spotify, Apple Music, and YouTube Music.

```
$ bs "https://music.apple.com/us/album/bimbambau/1895056025?i=6762879197"

              via Apple Music
  ▇           BIMBAMBAU   ✓ copied YouTube Music ♫
  █ ▆   ▄ ▂   Cain Culto
  █ █ █ █ █   BIMBAMBAU - Single · 2:07

    1 ● Spotify          open.spotify.com/track/0LA6vr…
    2 ● Apple Music      music.apple.com/…/bimbambau
  ┃   › ● YouTube Music  youtube.com/watch?v=Kf9jrscvBk8

  enter copy  ·  x copy & quit  ·  ? more
```

## Install

```bash
go install github.com/smithbr/bside@latest
```

## Usage

```
bside [-to spotify|apple|youtube] [-all] [link]
bside setup
```

## Spotify credentials

Searching Spotify needs an app from the [Spotify developer dashboard](https://developer.spotify.com/dashboard), and Spotify only lets an app use the Web API when the account that owns it has Premium. Run this once:

```bash
bside setup
```

It opens the dashboard, tells you what to put in each field, checks the Client ID and secret you paste back, and saves them in bside's config file, `~/.config/bside/bside.json` (or under `$XDG_CONFIG_HOME` if you set it), readable only by you.

`SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET` take precedence over the saved file.

```bash
export SPOTIFY_CLIENT_ID=your-client-id
export SPOTIFY_CLIENT_SECRET=your-client-secret
bs "$url"
```
