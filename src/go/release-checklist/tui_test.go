// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// colours matches what a style adds around the words. Stripping it lets a
// test look for what is on the screen whatever the terminal supports.
var colours = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(view string) string { return colours.ReplaceAllString(view, "") }

// dashboardShowing is a dashboard that has read the checklist, with every
// step in the state the test names and the rest done.
func dashboardShowing(t *testing.T, states map[string]State) *dashboard {
	t.Helper()

	run := testRun(t, Minor)
	statuses := make(map[string]Status, len(checklist))

	for _, step := range checklist {
		state, named := states[step.ID]
		if !named {
			state = Done
		}

		statuses[step.ID] = Status{State: state, Detail: "what the check found for " + step.ID}
	}

	dash := newDashboard(t.Context(), run.Profile)
	dash.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	dash.Update(checklistRead{run: run, statuses: statuses, at: time.Now()})

	return dash
}

// press sends one key the way a terminal would.
func press(dash *dashboard, key string) tea.Cmd {
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}

	switch key {
	case "up":
		message = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		message = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		message = tea.KeyMsg{Type: tea.KeyEnter}
	}

	_, command := dash.Update(message)

	return command
}

// selectStep moves the selection to a step by its checklist number.
func selectStep(t *testing.T, dash *dashboard, id string) {
	t.Helper()

	for index, step := range checklist {
		if step.ID == id {
			dash.selected = index

			return
		}
	}

	t.Fatalf("the checklist has no step %s", id)
}

func TestTheDashboardShowsEveryStep(t *testing.T) {
	view := plain(dashboardShowing(t, nil).View())

	for _, step := range checklist {
		if !strings.Contains(view, step.Title) {
			t.Errorf("step %s, %s, is missing from the dashboard:\n%s", step.ID, step.Title, view)
		}
	}
}

func TestTheHeaderNamesTheReleaseAndTheProfile(t *testing.T) {
	view := plain(dashboardShowing(t, nil).View())

	if want := "1.25.0 · minor · test"; !strings.Contains(view, want) {
		t.Errorf("the header does not name %q:\n%s", want, view)
	}
}

func TestTheLegendNamesEveryState(t *testing.T) {
	view := plain(dashboardShowing(t, nil).View())

	for _, state := range legendOrder {
		if !strings.Contains(view, string(state)) {
			t.Errorf("the legend does not name %s:\n%s", state, view)
		}
	}
}

func TestTheDetailPaneFollowsTheSelection(t *testing.T) {
	dash := dashboardShowing(t, nil)
	press(dash, "down")

	second := checklist[1]
	if want := second.ID + ". " + second.Title; !strings.Contains(plain(dash.View()), want) {
		t.Errorf("the detail pane does not describe %q after moving down:\n%s", want, plain(dash.View()))
	}

	press(dash, "up")

	first := checklist[0]
	if want := first.ID + ". " + first.Title; !strings.Contains(plain(dash.View()), want) {
		t.Errorf("the detail pane does not describe %q after moving back:\n%s", want, plain(dash.View()))
	}
}

func TestTheSelectionStopsAtBothEnds(t *testing.T) {
	dash := dashboardShowing(t, nil)

	press(dash, "up")

	if dash.selected != 0 {
		t.Errorf("moving up from the first step selected %d, want 0", dash.selected)
	}

	for range len(checklist) + 2 {
		press(dash, "down")
	}

	if want := len(checklist) - 1; dash.selected != want {
		t.Errorf("moving down past the last step selected %d, want %d", dash.selected, want)
	}
}

func TestTheDetailPaneFillsInTheRelease(t *testing.T) {
	view := plain(dashboardShowing(t, nil).View())

	for _, placeholder := range []string{"{version}", "{tag}", "{branch}", "{line}", "{repo}"} {
		if strings.Contains(view, placeholder) {
			t.Errorf("the dashboard shows the placeholder %s instead of the release's own value:\n%s",
				placeholder, view)
		}
	}
}

func TestTheInstructionsKeyShowsTheStepsOwnInstructions(t *testing.T) {
	dash := dashboardShowing(t, nil)
	press(dash, "i")

	view := plain(dash.View())
	opening, _, _ := strings.Cut(strings.TrimSpace(fill(checklist[0].Doc.Instructions, dash.run)), " ")

	if !strings.Contains(view, opening) {
		t.Errorf("the instructions do not start with %q:\n%s", opening, view)
	}

	if !strings.Contains(view, "i back") {
		t.Errorf("the instructions do not say which key leaves them:\n%s", view)
	}

	press(dash, "i")

	if view := plain(dash.View()); !strings.Contains(view, "i instructions") {
		t.Errorf("pressing i again did not return to the list:\n%s", view)
	}
}

func TestTheListScrollsToKeepTheSelectionInView(t *testing.T) {
	dash := dashboardShowing(t, nil)
	dash.selected = len(checklist) - 1

	rows := dash.rows(2)
	shown := plain(strings.Join(rows, "\n"))

	if len(rows) != 2 {
		t.Fatalf("asked for 2 rows and got %d", len(rows))
	}

	if last := checklist[len(checklist)-1].Title; !strings.Contains(shown, last) {
		t.Errorf("the selected step %q scrolled out of view:\n%s", last, shown)
	}

	if first := checklist[0].Title; strings.Contains(shown, first) {
		t.Errorf("the list did not scroll; it still shows %q:\n%s", first, shown)
	}
}

func TestEnterRunsNothingWhenTheStepIsNotAvailable(t *testing.T) {
	blocked := "9"
	dash := dashboardShowing(t, map[string]State{blocked: Blocked})
	selectStep(t, dash, blocked)

	if command := press(dash, "enter"); command != nil {
		t.Error("enter started an action for a blocked step")
	}

	if !strings.Contains(dash.notice, string(Blocked)) {
		t.Errorf("enter on a blocked step said %q, which does not say it is blocked", dash.notice)
	}
}

func TestEnterOnAStepDoneByHandPointsAtItsInstructions(t *testing.T) {
	dash := dashboardShowing(t, nil)

	byHand := ""

	for _, step := range checklist {
		if step.Action == nil {
			byHand = step.ID

			break
		}
	}

	if byHand == "" {
		t.Skip("every step the checklist ships has automation")
	}

	selectStep(t, dash, byHand)
	dash.statuses[byHand] = Status{State: Available}

	if command := press(dash, "enter"); command != nil {
		t.Errorf("enter started an action for step %s, which has none", byHand)
	}

	if !strings.Contains(dash.notice, "instructions") {
		t.Errorf("enter on step %s said %q, which does not point at its instructions", byHand, dash.notice)
	}
}

func TestQuitting(t *testing.T) {
	dash := dashboardShowing(t, nil)

	command := press(dash, "q")
	if command == nil {
		t.Fatal("q did not quit")
	}

	if message := command(); message != (tea.QuitMsg{}) {
		t.Errorf("q sent %T, want tea.QuitMsg", message)
	}
}

func TestAFailedReadIsShownInsteadOfAnEmptyChecklist(t *testing.T) {
	dash := newDashboard(t.Context(), &Profile{Name: "test"})
	dash.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	dash.Update(checklistRead{at: time.Now(), err: errors.New("gh is not logged in")})

	if view := plain(dash.View()); !strings.Contains(view, "gh is not logged in") {
		t.Errorf("the dashboard hides why the read failed:\n%s", view)
	}
}

// TestTheConfirmationAndTheWaitShareOneReader covers the assumption that lets
// stepAction hand RunAction its own buffered reader: bufio.NewReader gives
// back a reader it is given, so the line the confirmation did not consume is
// still there for the wait afterwards. Two readers over one terminal would
// swallow it.
func TestTheConfirmationAndTheWaitShareOneReader(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("y\nthe line after it\n"))

	if !confirmed(reader) {
		t.Fatal("the confirmation did not read the answer")
	}

	rest, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("the read after the confirmation got nothing: %v", err)
	}

	if want := "the line after it"; strings.TrimSpace(rest) != want {
		t.Errorf("the read after the confirmation got %q, want %q", strings.TrimSpace(rest), want)
	}
}

// TestAnActionKeepsTheTerminalUntilTheReaderLeaves covers the pause that
// stops the dashboard painting over what the operations printed.
func TestAnActionKeepsTheTerminalUntilTheReaderLeaves(t *testing.T) {
	run := testRun(t, Minor)
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, nil)
	step.Action = &Action{
		Title: "Do nothing at all",
		Plan:  func(context.Context, *Run) ([]Operation, error) { return nil, nil },
	}

	shown := &strings.Builder{}
	action := &stepAction{ctx: t.Context(), step: step, run: run}
	action.SetStdin(strings.NewReader("y\n\n"))
	action.SetStdout(shown)
	action.SetStderr(shown)

	if err := action.Run(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"Do nothing at all", "Press enter to return"} {
		if !strings.Contains(shown.String(), want) {
			t.Errorf("the action did not show %q:\n%s", want, shown.String())
		}
	}
}

func TestMarkingAStepThatNeedsNoJudgmentSaysSo(t *testing.T) {
	dash := dashboardShowing(t, nil)
	selectStep(t, dash, "1")

	command := press(dash, "m")
	if command == nil {
		t.Fatal("m on the release branch step started nothing")
	}

	dash.Update(command())

	if !strings.Contains(plain(dash.View()), "nothing to mark") {
		t.Errorf("the dashboard said nothing about the key:\n%s", plain(dash.View()))
	}
}
