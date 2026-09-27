package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/smithbr/bside/internal/music"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: bs [flags] [link]
       bs setup

Turns a song link from one streaming platform into links for the others.
Supported: Spotify, Apple Music, YouTube Music. With no link, bs asks you
to paste one.

Flags:
  -to <platform>  print only this platform's link (spotify, apple, youtube)
  -all            print every link found without the interactive list

Searching Spotify needs a Spotify developer app (Premium only): run "bs setup" once,
or set SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET.
`)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrProgramKilled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "setup" {
		return runSetup(ctx)
	}
	fs := flag.NewFlagSet("bs", flag.ContinueOnError)
	fs.Usage = usage
	to := fs.String("to", "", "")
	all := fs.Bool("all", false, "")

	// Allow flags before or after the link.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positional) > 1 {
		usage()
		os.Exit(2)
	}

	*to = strings.ToLower(*to)
	providers := music.Providers()
	if *to != "" && !knownProvider(providers, *to) {
		return fmt.Errorf("unknown platform %q (want spotify, apple, or youtube)", *to)
	}
	interactive := *to == "" && !*all && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))

	if len(positional) == 0 {
		if interactive {
			return runTUI(ctx, providers, nil, nil)
		}
		usage()
		os.Exit(2)
	}

	source, u, err := music.Source(providers, positional[0])
	if err != nil {
		return err
	}
	if interactive {
		return runTUI(ctx, providers, source, u)
	}

	track, err := source.Lookup(ctx, u)
	if err != nil {
		return fmt.Errorf("%s: %w", source.Name(), err)
	}
	results := music.FindAll(ctx, providers, source, track)

	var found []music.Result
	for _, r := range results {
		if r.Err == nil {
			found = append(found, r)
		} else if *to == "" || *to == r.Provider.ID() {
			fmt.Fprintf(os.Stderr, "%s: %v\n", r.Provider.Name(), r.Err)
		}
	}

	switch {
	case *to != "":
		if *to == source.ID() {
			fmt.Println(track.URL)
			return nil
		}
		for _, r := range found {
			if r.Provider.ID() == *to {
				fmt.Println(r.Track.URL)
				return nil
			}
		}
		return fmt.Errorf("not found on %s", *to)
	case len(found) == 0:
		return errors.New("not found on any other platform")
	}
	printAll(found)
	return nil
}

func knownProvider(providers []music.Provider, id string) bool {
	for _, p := range providers {
		if p.ID() == id {
			return true
		}
	}
	return false
}

func printAll(found []music.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range found {
		fmt.Fprintf(w, "%s\t%s\n", r.Provider.Name(), r.Track.URL)
	}
	w.Flush()
}
