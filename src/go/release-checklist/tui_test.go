// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
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

	return dashboardReading(t, testRun(t, Minor), states)
}

// dashboardReading is dashboardShowing for a run the test has set up.
func dashboardReading(t *testing.T, run *Run, states map[string]State) *dashboard {
	t.Helper()

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

// TestAReadOvertakenByALaterOneIsDropped covers two reads finishing out of
// order, as they can when r is pressed while a slow read is still out.
func TestAReadOvertakenByALaterOneIsDropped(t *testing.T) {
	dash := dashboardShowing(t, nil)

	reads := 0
	dash.refresh = func(context.Context) (*Run, error) {
		reads++

		return nil, fmt.Errorf("read %d failed", reads)
	}

	first, second := press(dash, "r"), press(dash, "r")
	older, newer := first(), second()

	dash.Update(newer)
	dash.Update(older)

	if view := plain(dash.View()); !strings.Contains(view, "read 2 failed") {
		t.Errorf("the first read replaced the second:\n%s", view)
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
	run.Tools = &fakeTools{output: map[string]string{cleanTreeQuery: ""}}
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, nil)
	step.Action = &Action{
		Title: "Do nothing at all",
		Plan:  func(context.Context, *Run) ([]Operation, error) { return nil, nil },
	}

	shown := &strings.Builder{}
	action := &stepAction{
		ctx:     t.Context(),
		step:    step,
		version: run.Release.Version,
		refresh: func(context.Context) (*Run, error) { return run, nil },
	}
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

// TestAnActionWorksFromTheReleaseAsItIsNow covers a dashboard left open while
// somebody pushed to the release branch. Tagging the head it shows would
// leave their commit out of the release.
func TestAnActionWorksFromTheReleaseAsItIsNow(t *testing.T) {
	then := &fakeTools{output: readyToTag(packageRunJSON("completed", "success")), anyCommand: true}
	dash := dashboardReading(t, tagRun(t, then, map[Version]string{}),
		map[string]State{tagRelease.ID: Available})

	moved := "cafecafecafecafecafecafecafecafecafecafe"
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers["gh api repos/"+testRepo+"/compare/"+moved+"...main --jq .behind_by"] = "2\n"
	answers[runsQueryAt(testBranch, moved)] = packageRunJSON("in_progress", "")
	now := &fakeTools{output: answers}
	dash.refresh = func(context.Context) (*Run, error) {
		return checklistRun(t, testRelease, map[Line]string{testRelease.Line(): moved}, now), nil
	}

	action := dash.actionFor(tagRelease)
	action.SetStdin(strings.NewReader("y\n\n"))
	action.SetStdout(io.Discard)

	if err := action.Run(); err == nil || !strings.Contains(err.Error(), string(Waiting)) {
		t.Errorf("tagging a branch whose new head is still building gave %v", err)
	}

	for _, call := range slices.Concat(then.calls, now.calls) {
		if strings.HasPrefix(call, "git push") {
			t.Errorf("the action ran %q", call)
		}
	}
}

func TestAnActionRunsNothingForAReleaseOtherThanTheOneShown(t *testing.T) {
	dash := dashboardShowing(t, nil)

	next := testRun(t, Patch)
	tools := &fakeTools{anyCommand: true}
	next.Tools = tools
	dash.refresh = func(context.Context) (*Run, error) { return next, nil }

	action := dash.actionFor(twoCommands())
	action.SetStdin(strings.NewReader("y\n\n"))
	action.SetStdout(io.Discard)

	if err := action.Run(); err == nil || !strings.Contains(err.Error(), next.Release.Version.String()) {
		t.Errorf("an action chosen on %s, with %s now in progress, gave %v",
			dash.run.Release.Version, next.Release.Version, err)
	}

	if len(tools.calls) != 0 {
		t.Errorf("ran %v for a release nobody chose", tools.calls)
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

// TestAMarkGoesToTheChecklistTheKeyWasPressedOn covers a read that finishes
// between the key press and the mark, which runs on a goroutine of its own.
func TestAMarkGoesToTheChecklistTheKeyWasPressedOn(t *testing.T) {
	shown := notesRun(t, theNotes, theNotes)
	dash := dashboardReading(t, shown, map[string]State{releaseNotes.ID: Available})
	selectStep(t, dash, releaseNotes.ID)

	mark := press(dash, "m")
	if mark == nil {
		t.Fatal("m on the release notes started nothing")
	}

	later := notesRun(t, theNotes, theNotes)
	dash.Update(checklistRead{run: later, at: time.Now()})

	if marked := mark().(stepChanged); marked.err != nil {
		t.Fatal(marked.err)
	}

	if _, marked := shown.Confirmation(releaseNotes.ID); !marked {
		t.Error("the mark missed the checklist the key was pressed on")
	}

	if _, marked := later.Confirmation(releaseNotes.ID); marked {
		t.Error("the mark went to a read that finished after the key press")
	}
}

// TestAKeyWaitsForTheCommandStillRunning covers enter, m or f pressed while
// a command still runs, as when the first press seemed to do nothing.
// Commands run on goroutines of their own, and two marks on one checklist
// write its confirmations at once.
func TestAKeyWaitsForTheCommandStillRunning(t *testing.T) {
	for _, first := range []struct {
		key      string
		finished tea.Msg
	}{
		{"enter", stepChanged{}},
		{"m", stepChanged{}},
		{"f", factsGathered{}},
	} {
		t.Run(first.key, func(t *testing.T) {
			dash := dashboardShowing(t, map[string]State{tagRelease.ID: Available})
			selectStep(t, dash, tagRelease.ID)

			if press(dash, first.key) == nil {
				t.Fatalf("%s started nothing", first.key)
			}

			for _, key := range []string{"enter", "m", "f"} {
				if press(dash, key) != nil {
					t.Errorf("%s started a command while %s's was still running", key, first.key)
				}

				if !strings.Contains(dash.notice, "step "+tagRelease.ID) {
					t.Errorf("%s said %q, which does not name what is still running", key, dash.notice)
				}
			}

			dash.Update(first.finished)

			if press(dash, "m") == nil {
				t.Errorf("m started nothing after %s's command finished", first.key)
			}
		})
	}
}

// TestTheDashboardFitsItsScreen keeps the notice counted in the height the
// list is given. A view taller than the screen loses its top lines, so the
// header scrolls away.
func TestTheDashboardFitsItsScreen(t *testing.T) {
	dash := dashboardShowing(t, nil)

	for _, notice := range []string{
		"",
		"step 1 is settled by its check, so there is nothing to mark",
		strings.Repeat("a notice long enough to wrap on any terminal ", 4),
	} {
		dash.notice = notice

		if lines := strings.Count(plain(dash.View()), "\n") + 1; lines != dash.height {
			t.Errorf("the dashboard drew %d lines on a %d line screen saying %q",
				lines, dash.height, notice)
		}
	}
}

func TestGatheringFactsForAStepWithNoneSaysSo(t *testing.T) {
	dash := dashboardShowing(t, nil)
	selectStep(t, dash, "1")

	if view := plain(dash.View()); !strings.Contains(view, "f gather facts") {
		t.Errorf("the footer does not name the key:\n%s", view)
	}

	command := press(dash, "f")
	if command == nil {
		t.Fatal("f on the release branch step started nothing")
	}

	dash.Update(command())

	if !strings.Contains(plain(dash.View()), "gathers nothing") {
		t.Errorf("the dashboard said nothing about the key:\n%s", plain(dash.View()))
	}
}

// TestFactsComeFromTheChecklistTheKeyWasPressedOn covers a read that finishes
// between pressing f and the gathering. Neither release is tagged, so the
// error names the release the gathering read.
func TestFactsComeFromTheChecklistTheKeyWasPressedOn(t *testing.T) {
	dash := dashboardReading(t, checklistRun(t, testRelease, nil, &fakeTools{}), nil)
	selectStep(t, dash, windowsAssets.ID)

	gather := press(dash, "f")
	if gather == nil {
		t.Fatal("f on the Windows assets started nothing")
	}

	next := Version{Major: 1, Minor: 25, Patch: 1}
	dash.Update(checklistRead{run: checklistRun(t, next, nil, &fakeTools{}), at: time.Now()})

	if gathered := gather().(factsGathered); gathered.err == nil ||
		!strings.Contains(gathered.err.Error(), testRelease.String()) {
		t.Errorf("gathering on %s, after a read of %s finished, gave %v", testRelease, next, gathered.err)
	}
}

func TestTheDashboardNamesTheFactsItGathered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dash := dashboardShowing(t, nil)
	dash.Update(factsGathered{path: filepath.Join(home, "Caches", factsFile)})

	if want := filepath.Join("~", "Caches", factsFile); !strings.Contains(plain(dash.View()), want) {
		t.Errorf("the dashboard does not name %s:\n%s", want, plain(dash.View()))
	}
}
