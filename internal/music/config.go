package music

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// config is bside's config file, bside.json.
type config struct {
	Spotify spotifyCredentials `json:"spotify"`
}

type spotifyCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// configDir is $XDG_CONFIG_HOME/bside, or ~/.config/bside like most
// command-line tools, including on macOS. Windows keeps its own config
// directory.
func configDir() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		var err error
		if runtime.GOOS == "windows" {
			dir, err = os.UserConfigDir()
		} else {
			dir, err = os.UserHomeDir()
			dir = filepath.Join(dir, ".config")
		}
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "bside"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bside.json"), nil
}

// legacyCredentialPaths are where earlier versions saved just the Spotify
// credentials, unnested, in spotify.json: under the config directory, and
// before that in Go's user config directory (~/Library/Application Support on
// macOS).
func legacyCredentialPaths() []string {
	var paths []string
	if dir, err := configDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "spotify.json"))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		if p := filepath.Join(dir, "bside", "spotify.json"); len(paths) == 0 || p != paths[0] {
			paths = append(paths, p)
		}
	}
	return paths
}

// loadConfig reads bside.json, moving credentials over from an older
// spotify.json the first time. A missing or unreadable file is an empty config.
func loadConfig() config {
	var c config
	path, err := configPath()
	if err != nil {
		return c
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return migrateConfig(path)
	}
	if err == nil {
		json.Unmarshal(b, &c)
		restrictPerms(path)
	}
	return c
}

// restrictPerms makes a config file someone created or copied by hand, and
// its directory, private again, since the file holds the Spotify secret.
// Windows doesn't use these permission bits.
func restrictPerms(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(path, 0o600)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(filepath.Dir(path), 0o700)
	}
}

// migrateConfig converts the first legacy spotify.json it finds into
// bside.json at path, then removes the old file.
func migrateConfig(path string) config {
	for _, old := range legacyCredentialPaths() {
		b, err := os.ReadFile(old)
		if err != nil {
			continue
		}
		var c config
		if json.Unmarshal(b, &c.Spotify) != nil {
			continue
		}
		if saveConfig(path, c) == nil {
			os.Remove(old)
			os.Remove(filepath.Dir(old)) // only succeeds if now empty
		}
		return c
	}
	return config{}
}

// saveConfig writes c to path, readable only by the user.
func saveConfig(path string, c config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Remove first so the new file gets 0600 even if an old one was wider.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
