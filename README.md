# bside

Turn a song or album link into links for the other streaming apps. Supports Spotify, Apple Music, and YouTube Music.

<img src="assets/demo.gif" alt="bs turning an Apple Music link for Welcome Wagon by Fang Island into Spotify and YouTube Music links" width="800">

## Install

```bash
brew install smithbr/tap/bs
```

Or with Go:

```bash
go install github.com/smithbr/bside/cmd/bs@latest
```

## Usage

```
bs [-to spotify|apple|youtube] [-all] [link]
bs setup
```

### Pasting links without quotes

zsh treats the `?` in song links as a wildcard and stops with `no matches found`. Add this to `~/.zshrc` to paste links as-is:

```bash
alias bs='noglob bs'
```

Or run `bs` with no link and paste it at the prompt.

## Spotify credentials

Searching Spotify needs an app from the [Spotify developer dashboard](https://developer.spotify.com/dashboard), and Spotify only lets an app use the Web API when the account that owns it has Premium. Run this once:

```bash
bs setup
```

It opens the dashboard, tells you what to put in each field, checks the Client ID and secret you paste back, and saves them in `~/.config/bside/bside.json` (or `$XDG_CONFIG_HOME/bside/bside.json` if set).

Or set `SPOTIFY_CLIENT_ID` and `SPOTIFY_CLIENT_SECRET`.

```bash
export SPOTIFY_CLIENT_ID=your-client-id && \
export SPOTIFY_CLIENT_SECRET=your-client-secret && \
bs https://open.spotify.com/tr...
```
