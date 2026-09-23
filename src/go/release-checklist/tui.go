// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// gutter is the two columns every line starts with: the selection bar
	// and the space after it.
	gutter = 2
	// labelWidth lines the detail pane's values up in one column.
	labelWidth = 12
	// clock is the time of the last refresh, which is all the reader needs
	// to tell a fresh checklist from one left on screen over lunch.
	clock = "15:04"
	// unread is the notice for a key pressed before the first refresh has
	// finished.
	unread = "the checklist has not been read yet"
)

// The dashboard paints in the terminal's own colours, so it follows the
// reader's theme rather than imposing one.
var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	strong = lipgloss.NewStyle().Bold(true)
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// mark is how one state shows in the list.
type mark struct {
	glyph string
	style lipgloss.Style
}

// marks give every state its glyph and colour. The list and the legend both
// render from here, so the two cannot come to disagree.
var marks = map[State]mark{
	Done:      {"✓", lipgloss.NewStyle().Foreground(lipgloss.Color("2"))},
	Available: {"▶", accent},
	Waiting:   {"⟳", lipgloss.NewStyle().Foreground(lipgloss.Color("3"))},
	Blocked:   {"·", lipgloss.NewStyle().Foreground(lipgloss.Color("1"))},
	Skipped:   {"–", dim},
	Unknown:   {"?", lipgloss.NewStyle().Foreground(lipgloss.Color("5"))},
}

// legendOrder is the order the legend names the states in: the ones a reader
// wants to act on first.
var legendOrder = []State{Done, Available, Waiting, Blocked, Skipped, Unknown}

// dashboard is the full-screen checklist: every step with its live state, a
// pane describing the selected one, and the keys that act on it.
type dashboard struct {
	// ctx is held here because bubbletea's model methods take none, and the
	// checks and actions the dashboard starts both need one.
	ctx context.Context
	// refresh reads the release in progress, which every check and action
	// works from. The tests answer in its place.
	refresh func(context.Context) (*Run, error)

	run        *Run
	statuses   map[string]Status
	refreshed  time.Time
	refreshing bool
	// failure is why the last refresh gave nothing to show.
	failure error
	// notice is what the last key press or action left to say.
	notice string

	selected     int
	instructions bool
	width        int
	height       int
}

// checklistRead carries a finished refresh back to the dashboard.
type checklistRead struct {
	run      *Run
	statuses map[string]Status
	at       time.Time
	err      error
}

// stepChanged reports that a step's automation finished, or that somebody
// marked the step done. Either changes what a check reads, so the checklist
// is read again.
type stepChanged struct{ err error }

// factsGathered carries back where a step's reference material was written.
// Gathering changes nothing a check reads, so no refresh follows it.
type factsGathered struct {
	path string
	err  error
}

func newDashboard(ctx context.Context, profile *Profile) *dashboard {
	return &dashboard{
		ctx:        ctx,
		refresh:    func(ctx context.Context) (*Run, error) { return refresh(ctx, profile) },
		refreshing: true,
	}
}

// showDashboard opens the checklist full screen. The dashboard reads the
// release itself rather than being handed one, so it appears at once and the
// checks run behind it.
func showDashboard(ctx context.Context, profile *Profile) error {
	_, err := tea.NewProgram(newDashboard(ctx, profile),
		tea.WithAltScreen(), tea.WithContext(ctx)).Run()

	return err
}

func (d *dashboard) Init() tea.Cmd { return d.read() }

// read finds the release and every step's state. The checks reach GitHub, so
// they run as a command rather than on the drawing path.
func (d *dashboard) read() tea.Cmd {
	return func() tea.Msg {
		run, err := d.refresh(d.ctx)
		if err != nil {
			return checklistRead{at: time.Now(), err: err}
		}

		statuses := make(map[string]Status, len(checklist))
		for _, step := range checklist {
			statuses[step.ID] = run.Status(d.ctx, step)
		}

		return checklistRead{run: run, statuses: statuses, at: time.Now()}
	}
}

func (d *dashboard) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height = message.Width, message.Height
	case checklistRead:
		d.run, d.statuses, d.refreshed = message.run, message.statuses, message.at
		d.failure, d.refreshing = message.err, false
	case stepChanged:
		d.notice = ""
		if message.err != nil {
			d.notice = message.err.Error()
		}

		read := d.startRead()

		return d, read

	case factsGathered:
		d.notice = "the facts are in " + atHome(message.path)
		if message.err != nil {
			d.notice = message.err.Error()
		}

	case tea.KeyMsg:
		return d.press(message)
	}

	return d, nil
}

// press acts on one key. Moving the selection and opening the instructions
// need nothing outside the dashboard; the rest start a command.
func (d *dashboard) press(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	d.notice = ""

	switch key.String() {
	case "q", "ctrl+c":
		return d, tea.Quit
	case "up", "k":
		d.selected = max(d.selected-1, 0)
	case "down", "j":
		d.selected = min(d.selected+1, len(checklist)-1)
	case "i":
		d.instructions = !d.instructions
	case "r":
		read := d.startRead()

		return d, read
	case "enter":
		action := d.runSelected()

		return d, action
	case "m":
		mark := d.markSelected()

		return d, mark
	case "f":
		gather := d.gatherSelected()

		return d, gather
	}

	return d, nil
}

func (d *dashboard) startRead() tea.Cmd {
	d.refreshing = true

	return d.read()
}

// runSelected hands the terminal to the selected step's automation. The
// dashboard cannot ask the question the action asks, nor show what the
// commands print, so the action runs against the real terminal with the
// dashboard suspended.
func (d *dashboard) runSelected() tea.Cmd {
	step := checklist[d.selected]

	switch {
	case d.run == nil:
		d.notice = unread
	case step.Action == nil:
		d.notice = fmt.Sprintf("step %s is done by hand; press i for its instructions", step.ID)
	case d.statuses[step.ID].State != Available:
		d.notice = fmt.Sprintf("step %s is %s, so there is nothing to run",
			step.ID, d.statuses[step.ID].State)
	default:
		return tea.Exec(d.actionFor(step), func(err error) tea.Msg { return stepChanged{err: err} })
	}

	return nil
}

// actionFor is a step's automation, chosen from the release on screen.
func (d *dashboard) actionFor(step *Step) *stepAction {
	return &stepAction{ctx: d.ctx, step: step, version: d.run.Release.Version, refresh: d.refresh}
}

// markSelected marks the selected step done, or takes the mark off when it is
// already marked for the text it would mark now. Reading that text reaches
// GitHub, so the mark is made in a command rather than on the drawing path.
func (d *dashboard) markSelected() tea.Cmd {
	if d.run == nil {
		d.notice = unread

		return nil
	}

	step := checklist[d.selected]

	return func() tea.Msg { return stepChanged{err: Mark(d.ctx, step, d.run)} }
}

// gatherSelected writes the selected step's reference material. Gathering
// reaches GitHub, so it runs in a command rather than on the drawing path.
func (d *dashboard) gatherSelected() tea.Cmd {
	if d.run == nil {
		d.notice = unread

		return nil
	}

	step := checklist[d.selected]

	return func() tea.Msg {
		path, err := GatherFacts(d.ctx, step, d.run)

		return factsGathered{path: path, err: err}
	}
}

// stepAction runs one step's automation while the dashboard is suspended. It
// satisfies tea.ExecCommand, which is how bubbletea lends out the terminal.
type stepAction struct {
	ctx  context.Context
	step *Step
	// version is the release the step was chosen in.
	version Version
	refresh func(context.Context) (*Run, error)

	in  *bufio.Reader
	out io.Writer
}

// SetStdin buffers the terminal once. RunAction reads the confirmation from
// the same reader, because bufio.NewReader hands back a reader it is given,
// so nothing it buffered is lost before the wait below.
func (a *stepAction) SetStdin(in io.Reader) { a.in = bufio.NewReader(in) }

func (a *stepAction) SetStdout(out io.Writer) { a.out = out }

func (a *stepAction) SetStderr(io.Writer) {}

// Run asks the action's one question, runs its operations, and waits for the
// reader. The dashboard paints over everything printed here as soon as it
// comes back, so leaving is the reader's to decide.
func (a *stepAction) Run() error {
	err := a.runFresh()
	if err != nil {
		fmt.Fprintf(a.out, "\n%v\n", err)
	}

	fmt.Fprint(a.out, "\nPress enter to return to the checklist. ")

	_, _ = a.in.ReadString('\n')

	return err
}

// runFresh reads the release again and runs the action against that read.
// The dashboard's read can be hours old, and the tag step would push the
// branch head it saw, missing any commit pushed since.
func (a *stepAction) runFresh() error {
	fmt.Fprintln(a.out, "Reading the checklist again…")

	run, err := a.refresh(a.ctx)
	if err != nil {
		return err
	}

	if run.Release.Version != a.version {
		return fmt.Errorf("the checklist showed %s, but the release in progress is now %s; nothing ran",
			a.version, run.Release.Version)
	}

	return RunAction(a.ctx, a.step, run, a.in, a.out)
}

func (d *dashboard) View() string {
	switch {
	case d.width == 0:
		return ""
	case d.run == nil && d.failure != nil:
		return fmt.Sprintf("\n  reading the checklist: %v\n\n%s\n",
			d.failure, strings.Join(d.footerLines(), "\n"))
	case d.run == nil:
		return "\n  Reading the checklist…\n"
	}

	if d.instructions {
		return d.instructionsView()
	}

	rule := dim.Render(strings.Repeat("─", max(d.width-gutter, 1)))

	above := []string{d.header(), rule}

	below := append([]string{rule}, d.detail()...)
	below = append(below, "")
	below = append(below, d.footerLines()...)
	below = append(below, d.noticeLines()...)
	below = append(below, "", d.legend())

	rows := d.rows(max(d.height-len(above)-len(below), 1))

	return strings.Join(slices.Concat(above, rows, below), "\n")
}

// instructionsView gives the whole screen to the selected step's manual
// instructions, because they are there to be read rather than glanced at.
func (d *dashboard) instructionsView() string {
	step := checklist[d.selected]

	lines := []string{d.header(), "", d.title(step), ""}
	lines = append(lines, d.wrap(fill(step.Doc.Instructions, d.run))...)
	lines = append(lines, "")
	lines = append(lines, d.footerLines()...)
	lines = append(lines, d.noticeLines()...)

	return strings.Join(lines, "\n")
}

// header names the release being driven and when the checklist was read, so
// a run against a fork profile cannot be mistaken for the real thing.
func (d *dashboard) header() string {
	release := d.run.Release

	left := fmt.Sprintf(" %s · %s · %s", release.Version, release.State(), d.run.Profile.Name)

	if warnings := len(release.Warnings); warnings > 0 {
		left += fmt.Sprintf("  ! %s", strings.Join(release.Warnings, "; "))
	}

	right := "refreshed " + d.refreshed.Format(clock)
	if d.refreshing {
		right = "reading…"
	}

	pad := max(d.width-lipgloss.Width(left)-len(right)-1, 1)

	return left + strings.Repeat(" ", pad) + dim.Render(right)
}

// rows renders the steps that fit, scrolled to keep the selected one in view.
func (d *dashboard) rows(height int) []string {
	titles := 0
	for _, step := range checklist {
		titles = max(titles, lipgloss.Width(step.Title))
	}

	first := max(d.selected-height+1, 0)
	rows := make([]string, 0, height)

	for index := first; index < min(first+height, len(checklist)); index++ {
		rows = append(rows, d.row(checklist[index], index == d.selected, titles))
	}

	// The list keeps the height it is given, so the rule under it and the
	// pane below stay where the reader last saw them.
	for len(rows) < height {
		rows = append(rows, "")
	}

	return rows
}

func (d *dashboard) row(step *Step, selected bool, titles int) string {
	status := d.statuses[step.ID]
	title := step.Title + strings.Repeat(" ", titles-lipgloss.Width(step.Title))

	bar := "  "
	if selected {
		bar, title = accent.Render("▌")+" ", strong.Render(title)
	}

	line := fmt.Sprintf("%s%s %s  %s", bar, glyph(status.State), title, dim.Render(status.Detail))

	return strings.TrimRight(lipgloss.NewStyle().MaxWidth(d.width).Render(line), " ")
}

// detail describes the selected step: what its live state means, and what the
// step reference in the README says about it, filled in for this release.
func (d *dashboard) detail() []string {
	step := checklist[d.selected]
	status := d.statuses[step.ID]

	lines := []string{d.title(step)}

	for _, entry := range []struct{ label, body string }{
		{string(status.State), status.Detail},
		{"Done when", fill(step.Doc.Check, d.run)},
		{"Waits for", fill(step.Doc.Precondition, d.run)},
		{"Reaches", reaches(step)},
		{"Runs", fill(runs(step), d.run)},
		{"Gathers", gathers(step)},
	} {
		lines = append(lines, d.field(entry.label, entry.body)...)
	}

	return lines
}

// field is one labelled entry of the detail pane. A long value wraps under
// itself rather than under the label.
func (d *dashboard) field(label, body string) []string {
	width := max(d.width-gutter-labelWidth, 1)
	value := lipgloss.NewStyle().Width(width).Render(body)
	label = dim.Render(label + strings.Repeat(" ", max(labelWidth-len(label), 1)))

	block := lipgloss.JoinHorizontal(lipgloss.Top, label, value)

	return indent(strings.Split(block, "\n"))
}

func (d *dashboard) title(step *Step) string {
	return "  " + strong.Render(step.ID+". "+step.Title)
}

// wrap breaks prose to the screen and indents it into the gutter.
func (d *dashboard) wrap(text string) []string {
	width := max(d.width-gutter, 1)
	wrapped := lipgloss.NewStyle().Width(width).Render(text)

	return indent(strings.Split(wrapped, "\n"))
}

// footerLines names the keys that do something here, broken to the screen
// like the notice, because the keys do not fit one line of a narrow
// terminal.
func (d *dashboard) footerLines() []string {
	keys := "enter run · m mark done · f gather facts · i instructions · r refresh · q quit"
	if d.instructions {
		keys = "i back · r refresh · q quit"
	}

	lines := d.wrap(keys)
	for index, line := range lines {
		lines[index] = dim.Render(line)
	}

	return lines
}

// atHome shortens a path under the home directory the way a reader would
// type it, so a notice naming a path fits the screen.
func atHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return path
	}

	return "~" + strings.TrimPrefix(path, home)
}

// noticeLines breaks the notice to the screen, one line per element, so the
// view can count them and the list above keeps the height it was given.
func (d *dashboard) noticeLines() []string {
	if d.notice == "" {
		return nil
	}

	return append([]string{""}, d.wrap(d.notice)...)
}

// legend spells out the glyphs in the list.
func (d *dashboard) legend() string {
	named := make([]string, 0, len(legendOrder))
	for _, state := range legendOrder {
		named = append(named, glyph(state)+" "+string(state))
	}

	return "  " + dim.Render(strings.Join(named, "  "))
}

// glyph is the mark one state shows.
func glyph(state State) string {
	shown, known := marks[state]
	if !known {
		shown = marks[Unknown]
	}

	return shown.style.Render(shown.glyph)
}

// indent moves a block into the gutter every other line starts in, and drops
// the padding wrapping leaves at the end of a line, so nothing on screen
// carries trailing spaces into whatever the reader pastes it into.
func indent(lines []string) []string {
	for index, line := range lines {
		lines[index] = strings.TrimRight("  "+line, " ")
	}

	return lines
}
