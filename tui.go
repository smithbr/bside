package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/smithbr/bside/internal/music"
)

// Palette: a late-night record store. Warm amber and coral on the terminal's own
// background, with each platform wearing its brand color.
var (
	amber  = lipgloss.AdaptiveColor{Light: "#B8660B", Dark: "#FFB454"}
	coral  = lipgloss.AdaptiveColor{Light: "#C8472B", Dark: "#FF8C61"}
	ink    = lipgloss.AdaptiveColor{Light: "#1C1B1A", Dark: "#F4EDE4"}
	muted  = lipgloss.AdaptiveColor{Light: "#76716A", Dark: "#8E877E"}
	faint  = lipgloss.AdaptiveColor{Light: "#B5AFA6", Dark: "#4A4540"}
	danger = lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#FF6B6B"}

	brand = map[string]lipgloss.AdaptiveColor{
		"spotify": {Light: "#12883E", Dark: "#1ED760"},
		"apple":   {Light: "#D6284A", Dark: "#FB5C74"},
		"youtube": {Light: "#CC0000", Dark: "#FF3D3D"},
	}

	eqColors = []lipgloss.AdaptiveColor{
		{Light: "#C98A00", Dark: "#FFD166"},
		amber,
		coral,
		{Light: "#C23B5A", Dark: "#FF6B8B"},
		{Light: "#A62E6B", Dark: "#F25FA8"},
	}

	// Footer toast fades from bright to nothing over these steps.
	toastFade = []lipgloss.AdaptiveColor{
		{Light: "#B8660B", Dark: "#FFD166"},
		{Light: "#B8660B", Dark: "#FFB454"},
		{Light: "#9C6A2E", Dark: "#D9A45A"},
		{Light: "#8C7A62", Dark: "#A88B62"},
		{Light: "#A39A8E", Dark: "#76675A"},
		{Light: "#BFB8AE", Dark: "#4F4740"},
	}
)

const (
	frameRate   = 90 * time.Millisecond
	eqBars      = 5
	eqHeight    = 3
	flashFrames = 5
	toastFrames = 24
	nameWidth   = 15
)

var (
	blocks     = []rune("▁▂▃▄▅▆▇█")
	rowSpinner = spinner.MiniDot.Frames
	notes      = []string{"♪", "♫", "♬", "♫"}
)

type rowState int

const (
	rowSearching rowState = iota
	rowFound
	rowOriginal
	rowMissing
)

type tuiRow struct {
	provider music.Provider
	state    rowState
	url      string
	err      error
	copiedAt int
	copyErr  error
}

type keyMap struct {
	Up, Down, Copy, Open, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding  { return []key.Binding{k.Up, k.Down, k.Copy, k.Open, k.Quit} }
func (k keyMap) FullHelp() [][]key.Binding { return [][]key.Binding{k.ShortHelp()} }

var keys = keyMap{
	Up:   key.NewBinding(key.WithKeys("up", "k", "shift+tab"), key.WithHelp("↑/k", "up")),
	Down: key.NewBinding(key.WithKeys("down", "j", "tab"), key.WithHelp("↓/j", "down")),
	Copy: key.NewBinding(key.WithKeys("enter", " ", "c", "y"), key.WithHelp("enter", "copy")),
	Open: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
	Quit: key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit")),
}

type (
	tickMsg   struct{}
	lookupMsg struct {
		track music.Track
		err   error
	}
	searchMsg struct {
		index int
		track music.Track
		err   error
	}
)

type model struct {
	ctx       context.Context
	providers []music.Provider
	source    music.Provider
	link      *url.URL

	track  music.Track
	loaded bool
	rows   []tuiRow
	cursor int
	moved  bool

	frame   int
	width   int
	help    help.Model
	toast   string
	toastAt int

	err      error
	quitting bool
}

func runTUI(ctx context.Context, providers []music.Provider, source music.Provider, link *url.URL) error {
	m := model{ctx: ctx, providers: providers, source: source, link: link, width: 80, help: newHelp(), toastAt: -toastFrames}
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return err
	}
	return final.(model).err
}

func newHelp() help.Model {
	h := help.New()
	h.ShortSeparator = "  ·  "
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(amber)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(muted)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(faint)
	return h
}

func tick() tea.Cmd {
	return tea.Tick(frameRate, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(), func() tea.Msg {
		t, err := m.source.Lookup(m.ctx, m.link)
		return lookupMsg{t, err}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.frame++
		return m, tick()

	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		return m, nil

	case lookupMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("%s: %w", m.source.Name(), msg.err)
			m.quitting = true
			return m, tea.Quit
		}
		m.track, m.loaded = msg.track, true
		var cmds []tea.Cmd
		for i, p := range m.providers {
			r := tuiRow{provider: p, copiedAt: -flashFrames}
			if p.ID() == m.source.ID() {
				r.state, r.url = rowOriginal, msg.track.URL
				m.cursor = i
			} else {
				cmds = append(cmds, m.search(i, p))
			}
			m.rows = append(m.rows, r)
		}
		return m, tea.Batch(cmds...)

	case searchMsg:
		r := &m.rows[msg.index]
		if msg.err != nil {
			r.state, r.err = rowMissing, msg.err
		} else {
			r.state, r.url = rowFound, msg.track.URL
			// Land on the first converted link unless the user has already moved.
			if !m.moved && m.rows[m.cursor].state == rowOriginal {
				m.cursor = msg.index
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, keys.Up):
			m.cursor, m.moved = m.next(-1), true
		case key.Matches(msg, keys.Down):
			m.cursor, m.moved = m.next(1), true
		case key.Matches(msg, keys.Copy):
			if r := m.selected(); r != nil {
				r.copyErr = clipboard.WriteAll(r.url)
				r.copiedAt = m.frame
				if r.copyErr == nil {
					m.toast = "copied " + r.provider.Name() + " link"
				} else {
					m.toast = "couldn't copy: " + r.copyErr.Error()
				}
				m.toastAt = m.frame
			}
		case key.Matches(msg, keys.Open):
			if r := m.selected(); r != nil {
				if err := openURL(r.url); err != nil {
					m.toast = "couldn't open: " + err.Error()
				} else {
					m.toast = "opening " + r.provider.Name()
				}
				m.toastAt = m.frame
			}
		}
	}
	return m, nil
}

func (m model) search(i int, p music.Provider) tea.Cmd {
	return func() tea.Msg {
		t, err := p.Search(m.ctx, m.track)
		return searchMsg{i, t, err}
	}
}

func (m *model) selected() *tuiRow {
	if !m.loaded || m.rows[m.cursor].url == "" {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m model) next(dir int) int {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].url != "" {
			return i
		}
	}
	return m.cursor
}

func openURL(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// View

func (m model) View() string {
	if m.err != nil {
		return ""
	}
	sections := []string{m.card()}
	if m.loaded {
		sections = append(sections, m.list())
	}
	if m.loaded && !m.quitting {
		sections = append(sections, m.footer())
	}
	return lipgloss.NewStyle().Padding(1, 2, 0, 2).Render(lipgloss.JoinVertical(lipgloss.Left, sections...)) + "\n"
}

// equalizer draws eqBars vertical bars eqHeight rows tall. It dances while
// searching and settles into a slow groove once everything is in.
func (m model) equalizer() string {
	speed := 0.55
	if m.loaded && !m.searching() {
		speed = 0.22
	}
	if m.quitting {
		speed = 0
	}
	t := float64(m.frame) * speed
	levels := eqHeight * len(blocks)

	lines := make([]string, eqHeight)
	for bar := range eqBars {
		fb := float64(bar)
		v := 0.5 + (math.Sin(t+fb*1.7)+math.Sin(t*0.63+fb*0.9))/4
		h := 1 + int(v*float64(levels-1))
		style := lipgloss.NewStyle().Foreground(eqColors[bar%len(eqColors)])
		for row := range eqHeight {
			fill := h - (eqHeight-1-row)*len(blocks)
			ch := " "
			if fill > 0 {
				ch = string(blocks[min(fill, len(blocks))-1])
			}
			lines[row] += style.Render(ch)
			if bar < eqBars-1 {
				lines[row] += " "
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) searching() bool {
	for _, r := range m.rows {
		if r.state == rowSearching {
			return true
		}
	}
	return false
}

func (m model) card() string {
	maxText := max(20, m.width-4-2*3-(eqBars*2-1)-3)
	var title, artist, meta string
	if m.loaded {
		title = lipgloss.NewStyle().Bold(true).Foreground(ink).Render(truncate(m.track.Title, maxText))
		artist = lipgloss.NewStyle().Foreground(amber).Render(truncate(m.track.Artist, maxText))

		var parts []string
		if m.track.Album != "" && m.track.Album != m.track.Title {
			parts = append(parts, m.track.Album)
		}
		if m.track.Duration > 0 {
			d := m.track.Duration.Round(time.Second)
			parts = append(parts, fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60))
		}
		parts = append(parts, "via ")
		prefix := truncate(strings.Join(parts, " · "), maxText-len(m.source.Name()))
		meta = lipgloss.NewStyle().Foreground(muted).Render(prefix) +
			lipgloss.NewStyle().Foreground(brand[m.source.ID()]).Render(m.source.Name())
	} else {
		title = shimmer("reading "+strings.ToLower(m.source.Name())+" link…", m.frame)
		artist = lipgloss.NewStyle().Foreground(faint).Render(m.link.Host)
	}
	text := lipgloss.JoinVertical(lipgloss.Left, title, artist, meta)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.equalizer(), "   ", text)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(faint).
		Padding(0, 2).
		MarginBottom(1).
		Render(body)
}

func (m model) list() string {
	urlWidth := max(16, m.width-4-2-2-nameWidth-2-14)
	var lines []string
	for i, r := range m.rows {
		color := brand[r.provider.ID()]
		active := i == m.cursor && !m.quitting
		flashing := m.frame-r.copiedAt < flashFrames && r.copyErr == nil

		pointer, bar := "  ", lipgloss.NewStyle().Foreground(color).Faint(true).Render("▎")
		name := lipgloss.NewStyle().Width(nameWidth).Foreground(muted)
		if active {
			pointer = lipgloss.NewStyle().Foreground(amber).Bold(true).Render("› ")
			bar = lipgloss.NewStyle().Foreground(color).Render("┃")
			name = name.Foreground(color).Bold(true)
		}

		var detail string
		switch r.state {
		case rowSearching:
			spin := lipgloss.NewStyle().Foreground(color).Render(rowSpinner[(m.frame+i*3)%len(rowSpinner)])
			detail = spin + " " + shimmer("searching…", m.frame+i*4)
		case rowMissing:
			var msg string
			switch {
			case errors.Is(r.err, music.ErrNotFound):
				msg = "not found"
			case errors.Is(r.err, music.ErrSpotifyCredentials):
				msg = "needs SPOTIFY_CLIENT_ID / SECRET"
			default:
				msg = r.err.Error()
			}
			detail = lipgloss.NewStyle().Foreground(faint).Render("✕ " + truncate(msg, urlWidth))
		default:
			link := truncate(strings.TrimPrefix(r.url, "https://"), urlWidth)
			style := lipgloss.NewStyle().Foreground(muted)
			if active {
				style = style.Foreground(ink)
			}
			if flashing {
				style = lipgloss.NewStyle().Background(color).Foreground(lipgloss.Color("#111111")).Bold(true)
				link = " " + link + " "
			}
			detail = style.Render(link)
			switch {
			case r.copyErr != nil && r.copiedAt >= 0:
				detail += lipgloss.NewStyle().Foreground(danger).Render("  ✕ copy failed")
			case r.copiedAt >= 0:
				detail += lipgloss.NewStyle().Foreground(color).Render("  ✓ copied")
			case r.state == rowOriginal:
				detail += lipgloss.NewStyle().Foreground(faint).Render("  ◆ original")
			}
		}
		lines = append(lines, pointer+bar+" "+name.Render(r.provider.Name())+" "+detail)
	}
	return strings.Join(lines, "\n")
}

func (m model) footer() string {
	var toast string
	if age := m.frame - m.toastAt; age < toastFrames && m.toast != "" {
		step := min(age*len(toastFade)/toastFrames, len(toastFade)-1)
		trail := strings.Repeat(" ", min(age/3, 4)) + notes[(age/2)%len(notes)]
		toast = lipgloss.NewStyle().Foreground(toastFade[step]).Render(m.toast + " " + trail)
	}
	return lipgloss.JoinVertical(lipgloss.Left, "", m.help.View(keys), toast)
}

// shimmer renders s in a muted tone with a bright highlight sweeping across it.
func shimmer(s string, frame int) string {
	runes := []rune(s)
	pos := frame % (len(runes) + 8)
	var b strings.Builder
	for i, r := range runes {
		style := lipgloss.NewStyle().Foreground(faint)
		switch d := i - pos + 3; {
		case d == 0:
			style = style.Foreground(ink)
		case d == -1 || d == 1:
			style = style.Foreground(amber)
		case d == -2 || d == 2:
			style = style.Foreground(muted)
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-1)]) + "…"
}
