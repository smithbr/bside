package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/smithbr/bside/internal/music"
)

const spotifyDashboard = "https://developer.spotify.com/dashboard/create"

// runSetup walks through creating a Spotify app and saves its credentials so
// bside can search Spotify without any environment variables.
func runSetup(ctx context.Context) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("setup is interactive; run it in a terminal")
	}
	// Ctrl-C is caught by ctx and can't interrupt a blocked read, so exit here,
	// restoring echo in case it lands while the secret is being typed.
	state, err := term.GetState(fd)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		term.Restore(fd, state)
		fmt.Println()
		os.Exit(130)
	}()
	in := bufio.NewReader(os.Stdin)

	fmt.Print(`Searching Spotify needs a Spotify developer app, and Spotify only allows
apps to use the Web API if the account that owns them has Premium. To create one:

  1. Log in and fill in the form:
       App name       bside
       Description    bside
       Redirect URI   http://127.0.0.1:8888/callback   (required, but unused)
       APIs used      Web API
  2. Accept the terms and click Save.
  3. Open Settings on the new app to find its Client ID and Client secret.

`)
	fmt.Printf("Press Enter to open %s ", spotifyDashboard)
	if _, err := readLine(in); err != nil {
		return err
	}
	if err := openURL(spotifyDashboard); err != nil {
		fmt.Println("Couldn't open a browser; visit the link above.")
	}
	fmt.Println()

	for {
		id, err := prompt(in, "Client ID: ")
		if err != nil {
			return err
		}
		secret, err := promptSecret("Client secret (hidden): ")
		if err != nil {
			return err
		}
		fmt.Print("Checking with Spotify… ")
		path, err := music.SetupSpotify(ctx, id, secret)
		if err == nil {
			fmt.Printf("done.\n\nSaved to %s. Spotify search is ready.\n", path)
			return nil
		}
		fmt.Printf("failed: %v\nDouble-check both values and try again (Ctrl-C to quit).\n\n", err)
	}
}

func prompt(in *bufio.Reader, label string) (string, error) {
	for {
		fmt.Print(label)
		s, err := readLine(in)
		if err != nil {
			return "", err
		}
		if s != "" {
			return s, nil
		}
	}
}

func promptSecret(label string) (string, error) {
	for {
		fmt.Print(label)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
}

func readLine(in *bufio.Reader) (string, error) {
	s, err := in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
