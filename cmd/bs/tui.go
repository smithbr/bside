package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/harmonica"

	"github.com/smithbr/bside/internal/music"
)

// palette: a late-night record store. Warm amber and coral on the terminal's
// own background, with each platform wearing its brand color.
type palette struct {
	amber, coral, ink, muted, faint, danger color.Color

	brand     map[string]color.Color
	eq        []color.Color
	toastFade []color.Color // a row's toast fades from bright to nothing
}

func newPalette(dark bool) palette {
	ld := lipgloss.LightDark(dark)
	c := func(light, dark string) color.Color { return ld(lipgloss.Color(light), lipgloss.Color(dark)) }
	p := palette{
		amber:  c("#B8660B", "#FFB454"),
		coral:  c("#C8472B", "#FF8C61"),
		ink:    c("#1C1B1A", "#F4EDE4"),
		muted:  c("#76716A", "#8E877E"),
		faint:  c("#B5AFA6", "#4A4540"),
		danger: c("#B3261E", "#FF6B6B"),
		brand: map[string]color.Color{
			"spotify": c("#12883E", "#1ED760"),
			"apple":   c("#D6284A", "#FB5C74"),
			"youtube": c("#CC0000", "#FF3D3D"),
		},
	}
	p.eq = []color.Color{c("#C98A00", "#FFD166"), p.amber, p.coral, c("#C23B5A", "#FF6B8B"), c("#A62E6B", "#F25FA8")}
	p.toastFade = lipgloss.Blend1D(6, c("#B8660B", "#FFD166"), p.faint)
	return p
}

const (
	frameRate   = 90 * time.Millisecond
	eqBars      = 5
	eqHeight    = 4 // as tall as the header: via, title, artist, album
	eqWidth     = eqBars*2 - 1
	toastFrames = 24
	nameWidth   = 15
	linkWidth   = 32
	maxNudge    = 2 // how far the selected row leans in, in columns

	// Results are held back so they arrive in a steady rhythm: nothing shows
	// before minScanFrames, then one row every staggerFrames, each typing on
	// over revealFrames.
	minScanFrames = 7
	staggerFrames = 2
	revealFrames  = 5
)

var (
	blocks     = []rune("▁▂▃▄▅▆▇█")
	notes      = []string{"♪", "♫", "♬", "♫"}
	rowSpinner = spinner.Points.Frames

	// writeClipboard is swapped out in tests so they leave the real one alone.
	writeClipboard = clipboard.WriteAll

	// The selected row springs into place: a quick lean with a little overshoot.
	nudgeSpring = harmonica.NewSpring(harmonica.FPS(int(time.Second/frameRate)), 9, 0.35)
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

	pending    *searchMsg // result waiting for its turn to be revealed
	revealedAt int
}

type keyMap struct {
	Up, Down, Copy, Number, CopyQuit, Open, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding { return []key.Binding{k.Copy, k.CopyQuit, k.Help} }
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down}, {k.Copy, k.Number, k.CopyQuit}, {k.Open, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:       key.NewBinding(key.WithKeys("up", "k", "shift+tab"), key.WithHelp("↑/k", "up")),
	Down:     key.NewBinding(key.WithKeys("down", "j", "tab"), key.WithHelp("↓/j", "down")),
	Copy:     key.NewBinding(key.WithKeys("enter", "space", "c", "y"), key.WithHelp("enter", "copy")),
	Number:   key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), key.WithHelp("1-9", "copy row")),
	CopyQuit: key.NewBinding(key.WithKeys("x", "shift+enter"), key.WithHelp("x", "copy & quit")),
	Open:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more")),
	Quit:     key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit")),
}

type (
	tickMsg    struct{}
	quitNowMsg struct{}
	lookupMsg  struct {
		gen   int
		track music.Track
		err   error
	}
	searchMsg struct {
		gen   int
		index int
		track music.Track
		err   error
	}
)

type model struct {
	ctx       context.Context
	providers []music.Provider
	source    music.Provider // nil until the user pastes a link
	link      *url.URL
	gen       int // bumped per lookup so a replaced link's results are dropped
	input     textinput.Model

	track  music.Track
	loaded bool
	rows   []tuiRow
	cursor int
	moved  bool
	hover  int // row under the mouse, or -1
	copied int // row last copied, or -1

	nudge, nudgeVel float64 // the selected row's spring-driven lean

	startFrame int // frame the lookup finished; reveals count from here
	lastReveal int

	pal      palette
	frame    int
	width    int
	height   int
	originY  int // screen row where the view starts, or -1 if unknown
	help     help.Model
	toast    string
	toastAt  int
	toastRow int // the row the toast is about; it shows at that row's end

	ticket   bool // quit via copy & quit, so leave the ticket behind
	answered bool // the terminal has replied to the startup queries
	err      error
	quitting bool
}

// runTUI shows the live results. With a nil source it first asks for a link.
func runTUI(ctx context.Context, providers []music.Provider, source music.Provider, link *url.URL) error {
	m := newModel(ctx, providers, source, link)
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return err
	}
	return final.(model).err
}

func newModel(ctx context.Context, providers []music.Provider, source music.Provider, link *url.URL) model {
	m := model{
		ctx: ctx, providers: providers, source: source, link: link,
		input: textinput.New(), help: help.New(), hover: -1, copied: -1,
		width: 80, originY: -1, toastAt: -toastFrames,
	}
	m.input.Prompt = ""
	m.input.Placeholder = "paste a Spotify, Apple Music, or YouTube Music link"
	m.input.Focus()
	m.help.ShortSeparator = "  ·  "
	m.setPalette(newPalette(true))
	keys.Number.SetHelp(fmt.Sprintf("1-%d", len(providers)), "copy row")
	return m
}

// setPalette restyles everything that depends on the terminal's background.
func (m *model) setPalette(p palette) {
	m.pal = p
	s := textinput.DefaultStyles(true)
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(p.faint)
	s.Focused.Text = lipgloss.NewStyle().Foreground(p.ink)
	s.Cursor.Color = p.amber
	m.input.SetStyles(s)

	m.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(p.amber)
	m.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(p.muted)
	m.help.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(p.faint)
	m.help.Styles.FullKey = m.help.Styles.ShortKey
	m.help.Styles.FullDesc = m.help.Styles.ShortDesc
	m.help.Styles.FullSeparator = m.help.Styles.ShortSeparator
}

func tick() tea.Cmd {
	return tea.Tick(frameRate, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), tea.RequestBackgroundColor, tea.RequestCursorPosition}
	if m.source == nil {
		cmds = append(cmds, textinput.Blink)
	} else {
		cmds = append(cmds, m.lookup())
	}
	return tea.Batch(cmds...)
}

func (m model) lookup() tea.Cmd {
	source, link, gen := m.source, m.link, m.gen
	return func() tea.Msg {
		t, err := source.Lookup(m.ctx, link)
		return lookupMsg{gen, t, err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.frame++
		m.reveal()
		m.nudge, m.nudgeVel = nudgeSpring.Update(m.nudge, m.nudgeVel, maxNudge)
		return m, tick()

	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width, m.height = msg.Width, msg.Height
		}
		return m, nil

	case tea.BackgroundColorMsg:
		m.setPalette(newPalette(msg.IsDark()))
		return m, nil

	case tea.CursorPositionMsg:
		if m.originY < 0 {
			m.originY = msg.Y
		}
		m.answered = true
		if m.quitting {
			return m, tea.Quit
		}
		return m, nil

	case quitNowMsg:
		return m, tea.Quit
	}

	if m.quitting {
		return m, nil // waiting on the terminal before exiting
	}

	if m.source == nil {
		return m.updateInput(msg)
	}

	switch msg := msg.(type) {
	case tea.PasteMsg:
		// Pasting another link over the results looks that one up instead.
		source, u, err := music.Source(m.providers, msg.Content)
		if err != nil || (m.link != nil && u.String() == m.link.String()) {
			return m, nil
		}
		return m.restart(source, u)

	case lookupMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.err != nil {
			m.err = fmt.Errorf("%s: %w", m.source.Name(), msg.err)
			return m.quit()
		}
		m.track, m.loaded = msg.track, true
		m.startFrame, m.lastReveal = m.frame, m.frame-staggerFrames
		var cmds []tea.Cmd
		for i, p := range m.providers {
			r := tuiRow{provider: p, revealedAt: -revealFrames}
			if p.ID() == m.source.ID() {
				r.state, r.url, r.revealedAt = rowOriginal, msg.track.URL, m.frame
				m.cursor = i
			} else {
				cmds = append(cmds, m.search(i, p))
			}
			m.rows = append(m.rows, r)
		}
		return m, tea.Batch(cmds...)

	case searchMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.rows[msg.index].pending = &msg
		return m, nil

	case tea.MouseMotionMsg:
		m.hover = m.rowAt(msg.Y)
		return m, nil

	case tea.MouseClickMsg:
		if i := m.rowAt(msg.Y); msg.Button == tea.MouseLeft && i >= 0 && m.rows[i].url != "" {
			m.moved = true
			m.choose(i)
			m.copy()
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return m.quit()
		case key.Matches(msg, keys.Up):
			m.moved = true
			m.choose(m.next(-1))
		case key.Matches(msg, keys.Down):
			m.moved = true
			m.choose(m.next(1))
		case key.Matches(msg, keys.Copy):
			m.copy()
		case key.Matches(msg, keys.CopyQuit):
			if m.copy() {
				m.ticket = true
				return m.quit()
			}
		case key.Matches(msg, keys.Number):
			// Jump straight to that row and copy it, if it has a link yet.
			if i := int(msg.String()[0] - '1'); m.loaded && i < len(m.rows) && m.rows[i].url != "" {
				m.moved = true
				m.choose(i)
				m.copy()
			}
		case key.Matches(msg, keys.Open):
			if r := m.selected(); r != nil {
				if err := openURL(r.url); err != nil {
					m.toast = "✕ couldn't open: " + err.Error()
				} else {
					m.toast = "↗ opening"
				}
				m.toastAt, m.toastRow = m.frame, m.cursor
			}
		case key.Matches(msg, keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			if m.help.ShowAll {
				keys.Help.SetHelp("?", "less")
			} else {
				keys.Help.SetHelp("?", "more")
			}
		}
	}
	return m, nil
}

// quit exits once the terminal has answered the startup queries (background
// color, cursor position). Quitting before the answers arrive leaves them
// for the shell to print as garbage, so wait for the cursor position, which
// the terminal sends last, or give up after a moment.
func (m model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.answered {
		return m, tea.Quit
	}
	return m, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return quitNowMsg{} })
}

// updateInput handles messages while the user is pasting a link.
func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc", "ctrl+c":
			return m.quit()
		case "enter":
			source, u, err := music.Source(m.providers, m.input.Value())
			if err != nil {
				return m, nil
			}
			m.source, m.link = source, u
			m.input.Blur()
			return m, m.lookup()
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// restart looks up a new link in place of the current one.
func (m model) restart(source music.Provider, u *url.URL) (tea.Model, tea.Cmd) {
	m.gen++
	m.source, m.link = source, u
	m.track, m.loaded, m.rows = music.Track{}, false, nil
	m.cursor, m.moved, m.hover, m.copied = 0, false, -1, -1
	m.nudge, m.nudgeVel = 0, 0
	return m, m.lookup()
}

// choose moves the cursor, restarting the lean when it lands somewhere new.
func (m *model) choose(i int) {
	if i != m.cursor {
		m.cursor, m.nudge, m.nudgeVel = i, 0, 0
	}
}

// reveal shows at most one held-back result per call, once the minimum scan
// time has passed and the previous row has had its moment.
func (m *model) reveal() {
	if !m.loaded || m.frame < m.startFrame+minScanFrames || m.frame < m.lastReveal+staggerFrames {
		return
	}
	for i := range m.rows {
		r := &m.rows[i]
		if r.pending == nil {
			continue
		}
		msg := *r.pending
		r.pending, r.revealedAt, m.lastReveal = nil, m.frame, m.frame
		if msg.err != nil {
			r.state, r.err = rowMissing, msg.err
			return
		}
		r.state, r.url = rowFound, msg.track.URL
		// Land on the first converted link unless the user has already moved.
		if !m.moved && m.rows[m.cursor].state == rowOriginal {
			m.choose(i)
		}
		return
	}
}

// progress counts searches that have been revealed, out of all searches.
func (m model) progress() (done, total int) {
	for _, r := range m.rows {
		if r.state != rowOriginal {
			total++
			if r.state != rowSearching {
				done++
			}
		}
	}
	return done, max(total, 1)
}

// copy puts the selected link on the clipboard and reports whether it worked.
func (m *model) copy() bool {
	r := m.selected()
	if r == nil {
		return false
	}
	if err := writeClipboard(r.url); err != nil {
		m.toast, m.toastAt, m.toastRow = "✕ couldn't copy: "+err.Error(), m.frame, m.cursor
		return false
	}
	m.toast, m.toastAt, m.toastRow, m.copied = "✓ copied", m.frame, m.cursor, m.cursor
	return true
}

func (m model) search(i int, p music.Provider) tea.Cmd {
	return func() tea.Msg {
		t, err := p.Search(m.ctx, m.track)
		return searchMsg{m.gen, i, t, err}
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

// rowAt maps a screen row to a result row, or -1. Inline programs get mouse
// positions relative to the screen, so this needs where the view starts: the
// cursor row at launch, or higher if the view pushed the screen up.
func (m model) rowAt(y int) int {
	if !m.loaded || m.originY < 0 {
		return -1
	}
	top := m.originY
	if m.height > 0 {
		top = min(top, m.height-lipgloss.Height(m.content()))
	}
	i := y - top - m.listTop()
	if i < 0 || i >= len(m.rows) {
		return -1
	}
	return i
}

// listTop is the first result row's line within the view: top padding, the
// header, and a blank line.
func (m model) listTop() int {
	return 1 + eqHeight + 1
}

// openURL opens a platform's link in the browser. Links can come from API
// responses, so only https ones are handed to the OS, which would otherwise
// launch local files or other schemes.
func openURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("not an https link: %q", raw)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u.String()).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u.String()).Start()
	default:
		return exec.Command("xdg-open", u.String()).Start()
	}
}

// View

func (m model) View() tea.View {
	v := tea.NewView(m.content())
	v.WindowTitle = "bside"
	if m.loaded {
		v.WindowTitle = "♫ " + m.track.Title + " — " + m.track.Artist
	}
	if m.loaded && !m.quitting {
		v.MouseMode = tea.MouseModeAllMotion
	}
	switch done, total := m.progress(); {
	case m.source != nil && !m.loaded:
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	case m.loaded && done < total:
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarDefault, 100*done/total)
	}
	return v
}

func (m model) content() string {
	page := lipgloss.NewStyle().Padding(1, 2, 0, 2)
	switch {
	case m.err != nil || (m.quitting && m.source == nil):
		return ""
	case m.quitting && m.ticket:
		return page.Render(m.ticketView()) + "\n"
	case m.quitting && m.loaded:
		return page.Render(m.receipt()) + "\n"
	}
	sections := []string{m.panel()}
	if m.loaded {
		sections = append(sections, "", m.help.View(keys))
	}
	return page.Render(lipgloss.JoinVertical(lipgloss.Left, sections...)) + "\n"
}

// panelWidth is the most room the UI's text gets, inside the page padding.
func (m model) panelWidth() int {
	return min(76, max(44, m.width-4))
}

// panel is the whole UI, unboxed: the now-playing header, then a row per
// platform, with a thick bar in the selected platform's color
// marking the selected row.
func (m model) panel() string {
	w := m.panelWidth()
	if !m.loaded {
		return m.header(w)
	}
	lines := []string{m.header(w), ""}
	for i, r := range m.rows {
		bar := "  "
		if i == m.cursor {
			bar = lipgloss.NewStyle().Foreground(m.pal.brand[r.provider.ID()]).Render("┃ ")
		}
		lines = append(lines, bar+m.row(i, r, w-2))
	}
	return strings.Join(lines, "\n")
}

// box draws a rounded frame around lines, each padded to w, with its color
// running from top to bottom. Divider lines become ├─ label ─┤ rules, or a
// dashed ├┄┄┄┤ perforation when the label is empty. Line sel gets thick
// edges in selColor.
func (m model) box(lines []string, w int, from, to color.Color, dividers map[int]string, sel int, selColor color.Color) string {
	grad := lipgloss.Blend1D(len(lines)+2, from, to)
	edge := func(i int) lipgloss.Style { return lipgloss.NewStyle().Foreground(grad[i]) }

	out := []string{edge(0).Render("╭" + strings.Repeat("─", w+2) + "╮")}
	for i, line := range lines {
		e := edge(i + 1)
		if label, ok := dividers[i]; ok {
			if label == "" {
				out = append(out, e.Render("├"+strings.Repeat("┄", w+2)+"┤"))
				continue
			}
			rule := strings.Repeat("─", max(0, w-lipgloss.Width(label)-1))
			out = append(out, e.Render("├─ ")+label+e.Render(" "+rule+"┤"))
			continue
		}
		left, right := e.Render("│"), e.Render("│")
		if i == sel {
			thick := lipgloss.NewStyle().Foreground(selColor)
			left, right = thick.Render("┃"), thick.Render("┃")
		}
		pad := strings.Repeat(" ", max(0, w-lipgloss.Width(line)))
		out = append(out, left+" "+line+pad+" "+right)
	}
	out = append(out, edge(len(lines)+1).Render("╰"+strings.Repeat("─", w+2)+"╯"))
	return strings.Join(out, "\n")
}

// header is the equalizer beside the song: title, artist, details.
func (m model) header(w int) string {
	p := m.pal
	textW := w - eqWidth - 3
	var via, title, artist, meta string
	switch {
	case m.loaded:
		title = p.gradient(truncate(m.track.Title, textW))
		artist = lipgloss.NewStyle().Foreground(p.amber).Render(truncate(m.track.Artist, textW))
		via = lipgloss.NewStyle().Foreground(p.muted).Render("via ") +
			lipgloss.NewStyle().Foreground(p.brand[m.source.ID()]).Render(m.source.Name())
		// Album · 2:07, where only the album gets shortened.
		var parts []string
		d := duration(m.track.Duration)
		if m.track.Album != "" && m.track.Album != m.track.Title {
			if room := textW - lipgloss.Width(d) - 3; room >= 4 {
				parts = append(parts, truncate(m.track.Album, room))
			}
		}
		if d != "" {
			parts = append(parts, d)
		}
		meta = lipgloss.NewStyle().Foreground(p.muted).Render(strings.Join(parts, " · "))
	case m.source == nil:
		in := m.input
		in.SetWidth(textW)
		title = in.View()
		artist = m.inputHint(textW)
	default:
		title = p.shimmer("reading "+strings.ToLower(m.source.Name())+" link…", m.frame)
		artist = lipgloss.NewStyle().Foreground(p.faint).Render(m.link.Host)
	}
	// Once the song is in, "via Apple Music" sits on its own line above it.
	text := lipgloss.JoinVertical(lipgloss.Left, title, artist, meta)
	if via != "" {
		text = lipgloss.JoinVertical(lipgloss.Left, via, title, artist, meta)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.equalizer(), "   ", text)
}

// row is one platform: number or pointer, the platform in its color, the link.
func (m model) row(i int, r tuiRow, w int) string {
	p := m.pal
	c := p.brand[r.provider.ID()]
	active := i == m.cursor
	room := w - 2 - 2 - nameWidth - maxNudge // what's left for the link and toast
	lw := min(linkWidth, max(12, room-12))

	// A toast about this row trails its link, which gives up space if needed.
	var toast string
	if age := m.frame - m.toastAt; i == m.toastRow && age < toastFrames && m.toast != "" {
		step := min(age*len(p.toastFade)/toastFrames, len(p.toastFade)-1)
		text := m.toast + " " + notes[(age/2)%len(notes)]
		lw = max(8, min(lw, room-2-lipgloss.Width(text)))
		toast = "  " + lipgloss.NewStyle().Foreground(p.toastFade[step]).Render(truncate(text, room-lw-2))
	}

	pointer := "  "
	if r.url != "" {
		pointer = lipgloss.NewStyle().Foreground(p.faint).Render(fmt.Sprintf("%d ", i+1))
	}
	dot := lipgloss.NewStyle().Foreground(c).Render("● ")
	// The lean eats into the name's padding, so the link column never moves.
	name := lipgloss.NewStyle().Width(nameWidth + maxNudge).Foreground(p.muted)
	lean := ""
	switch {
	case active:
		pointer = lipgloss.NewStyle().Foreground(c).Bold(true).Render("› ")
		name = name.Foreground(c).Bold(true)
		lean = strings.Repeat(" ", m.lean())
		name = name.Width(nameWidth + maxNudge - m.lean())
	case i == m.hover && r.url != "":
		name = name.Foreground(p.ink)
	case r.state == rowMissing:
		dot = lipgloss.NewStyle().Foreground(p.faint).Render("○ ")
		name = name.Foreground(p.faint)
	}

	var detail string
	switch r.state {
	case rowSearching:
		detail = lipgloss.NewStyle().Foreground(c).Render(rowSpinner[(m.frame+i*2)%len(rowSpinner)])
	case rowMissing:
		msg := "no match"
		if errors.Is(r.err, music.ErrSpotifyCredentials) {
			msg = "not set up · run bs setup"
		} else if !errors.Is(r.err, music.ErrNotFound) {
			msg = truncate(r.err.Error(), lw)
		}
		detail = lipgloss.NewStyle().Foreground(p.faint).Render(msg)
	default:
		label := truncate(shortLink(r.url), lw)
		style := lipgloss.NewStyle().Foreground(p.muted)
		if active {
			style = style.Foreground(p.ink)
		}
		text := style.Render(label)
		if age := m.frame - r.revealedAt; age < revealFrames {
			text = typeOn(label, age, style, c)
		}
		// The label is a terminal hyperlink to the full URL.
		detail = lipgloss.NewStyle().Hyperlink(r.url).Render(text)
		if toast != "" {
			detail += strings.Repeat(" ", max(0, lw-lipgloss.Width(label)))
		}
	}
	return lean + pointer + dot + name.Render(r.provider.Name()) + detail + toast
}

// lean is how far the selected row leans in right now: the spring's position,
// rounded to whole columns, with room for one column of overshoot.
func (m model) lean() int {
	return min(maxNudge+1, max(0, int(math.Round(m.nudge))))
}

// ticketView is what copy & quit leaves behind: a stub with the song on top and,
// below the perforation, where it went and the link that's on the clipboard.
func (m model) ticketView() string {
	p := m.pal
	r := m.rows[max(m.copied, 0)]
	to := p.brand[r.provider.ID()]
	w := min(m.panelWidth()-6, max(36, lipgloss.Width(shortLink(r.url))+12))

	title := lipgloss.NewStyle().Foreground(p.amber).Render("♫  ") + p.gradient(truncate(m.track.Title, w-3))
	sub := "   " + lipgloss.NewStyle().Foreground(p.amber).Render(truncate(m.track.Artist, w-3))
	if d := duration(m.track.Duration); d != "" {
		sub += lipgloss.NewStyle().Foreground(p.muted).Render(" · " + d)
	}
	route := lipgloss.NewStyle().Foreground(p.brand[m.source.ID()]).Render(m.source.Name()) +
		lipgloss.NewStyle().Foreground(p.muted).Render("  ──▶  ") +
		lipgloss.NewStyle().Foreground(to).Bold(true).Render(r.provider.Name())
	link := lipgloss.NewStyle().Foreground(p.ink).Hyperlink(r.url).Render(truncate(shortLink(r.url), w-11)) +
		lipgloss.NewStyle().Foreground(to).Render("  ✓ copied")

	lines := []string{title, sub, "", route, link}
	return m.box(lines, w, p.brand[m.source.ID()], to, map[int]string{2: ""}, -1, nil)
}

// receipt is what's left in scrollback after quitting without copy & quit:
// the song, then either the link copied last or every link found.
func (m model) receipt() string {
	p := m.pal
	lines := []string{
		lipgloss.NewStyle().Foreground(p.amber).Render("♫ ") + p.gradient(m.track.Title) +
			lipgloss.NewStyle().Foreground(p.muted).Render(" — ") +
			lipgloss.NewStyle().Foreground(p.amber).Render(m.track.Artist),
	}
	link := func(r tuiRow) string {
		return lipgloss.NewStyle().Foreground(p.muted).Hyperlink(r.url).Render(shortLink(r.url))
	}
	if m.copied >= 0 {
		r := m.rows[m.copied]
		lines = append(lines, lipgloss.NewStyle().Foreground(p.brand[r.provider.ID()]).Render("✓ copied "+r.provider.Name())+
			lipgloss.NewStyle().Foreground(p.faint).Render(" · ")+link(r))
	} else {
		for _, r := range m.rows {
			if r.url != "" {
				name := lipgloss.NewStyle().Width(nameWidth).Foreground(p.brand[r.provider.ID()]).Render(r.provider.Name())
				lines = append(lines, "  "+name+link(r))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// equalizer draws eqBars vertical bars eqHeight rows tall. It dances while
// searching and settles into a slow groove once everything is in.
func (m model) equalizer() string {
	speed := 0.55
	if m.loaded && !m.searching() {
		speed = 0.22
	}
	if m.quitting || m.source == nil {
		speed = 0
	}
	t := float64(m.frame) * speed
	levels := eqHeight * len(blocks)

	lines := make([]string, eqHeight)
	for bar := range eqBars {
		fb := float64(bar)
		v := 0.5 + (math.Sin(t+fb*1.7)+math.Sin(t*0.63+fb*0.9))/4
		h := 1 + int(v*float64(levels-1))
		style := lipgloss.NewStyle().Foreground(m.pal.eq[bar%len(m.pal.eq)])
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

// inputHint says whether the text typed so far is a link bside can read.
func (m model) inputHint(width int) string {
	p := m.pal
	v := strings.TrimSpace(m.input.Value())
	if v == "" {
		return lipgloss.NewStyle().Foreground(p.faint).Render("enter to convert · esc to quit")
	}
	src, _, err := music.Source(m.providers, v)
	if err != nil {
		return lipgloss.NewStyle().Foreground(p.faint).Render(truncate(err.Error(), width))
	}
	return lipgloss.NewStyle().Foreground(p.brand[src.ID()]).Render("✓ " + src.Name() + " link")
}

// gradient renders s bold, blending amber into coral across its letters.
func (p palette) gradient(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	colors := lipgloss.Blend1D(max(len(runes), 2), p.amber, p.coral)
	var b strings.Builder
	for i, r := range runes {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[i]).Render(string(r)))
	}
	return b.String()
}

// shimmer renders s in a muted tone with a bright highlight sweeping across it.
func (p palette) shimmer(s string, frame int) string {
	runes := []rune(s)
	pos := frame % (len(runes) + 8)
	var b strings.Builder
	for i, r := range runes {
		style := lipgloss.NewStyle().Foreground(p.faint)
		switch d := i - pos + 3; {
		case d == 0:
			style = style.Foreground(p.ink)
		case d == -1 || d == 1:
			style = style.Foreground(p.amber)
		case d == -2 || d == 2:
			style = style.Foreground(p.muted)
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

// typeOn renders s mid-reveal: written left to right, with the newest few
// characters in the platform's color before they settle into style.
func typeOn(s string, age int, style lipgloss.Style, c color.Color) string {
	runes := []rune(s)
	written := min(len(runes), len(runes)*(age+1)/revealFrames)
	edge := max(0, written-3)
	return style.Render(string(runes[:edge])) +
		lipgloss.NewStyle().Foreground(c).Render(string(runes[edge:written])) +
		strings.Repeat(" ", len(runes)-written)
}

func duration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// shortLink trims a canonical song URL to something that fits in a column
// while keeping the part a person recognizes.
func shortLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimPrefix(raw, "https://")
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "open.spotify.com" && len(parts) == 2:
		return host + "/" + parts[0] + "/" + truncate(parts[1], 7)
	case host == "music.apple.com" && len(parts) >= 4 && (parts[1] == "album" || parts[1] == "song"):
		// /us/album/<song slug>/<album id>?i=<track id>
		return host + "/…/" + parts[2]
	case host == "music.youtube.com" && u.Path == "/watch":
		// Drop "music." so the video ID fits the column; the link stays whole.
		return "youtube.com/watch?v=" + u.Query().Get("v")
	}
	last := parts[len(parts)-1]
	switch {
	case last == "":
		return host
	case len(parts) > 1:
		return host + "/…/" + last
	}
	return host + "/" + last
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-1)]) + "…"
}
