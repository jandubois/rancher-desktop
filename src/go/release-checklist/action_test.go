// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

// twoCommands is a step whose automation runs two commands, for the tests
// about what an action shows and what it runs.
func twoCommands() *Step {
	step := answering(Answer{Detail: "not done"}, Answer{OK: true}, []Kind{Minor, Patch}, nil)
	step.Action = &Action{
		Title: "Tag {version}",
		Plan: func(context.Context, *Run) ([]Operation, error) {
			return []Operation{
				command("", "git", "fetch", "origin"),
				command("", "git", "push", "origin", "HEAD:refs/tags/v1.25.0"),
			}, nil
		},
	}

	return step
}

func TestConfirmedTakesOnlyYes(t *testing.T) {
	for _, answer := range []string{"y\n", "yes\n", "YES\n", " y \n"} {
		if !confirmed(strings.NewReader(answer)) {
			t.Errorf("%q was not taken as yes", answer)
		}
	}

	for _, answer := range []string{"n\n", "no\n", "\n", "", "later\n", "Y E S\n"} {
		if confirmed(strings.NewReader(answer)) {
			t.Errorf("%q was taken as yes", answer)
		}
	}
}

func TestActionShowsEveryOperationAndRunsNothingWhenDeclined(t *testing.T) {
	run := testRun(t, Minor)
	tools := &fakeTools{anyCommand: true}
	run.Tools = tools

	var out strings.Builder
	if err := RunAction(context.Background(), twoCommands(), run, strings.NewReader("n\n"), &out); err != nil {
		t.Fatal(err)
	}

	shown := out.String()
	for _, want := range []string{"Tag 1.25.0", "git fetch origin", "HEAD:refs/tags/v1.25.0"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the confirmation does not show %q:\n%s", want, shown)
		}
	}

	if len(tools.calls) != 0 {
		t.Errorf("declining ran %v", tools.calls)
	}
}

func TestActionRunsTheOperationsInOrder(t *testing.T) {
	run := testRun(t, Minor)
	tools := &fakeTools{anyCommand: true}
	run.Tools = tools

	if err := RunAction(context.Background(), twoCommands(), run, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatal(err)
	}

	want := []string{"git fetch origin", "git push origin HEAD:refs/tags/v1.25.0"}
	if len(tools.calls) != len(want) {
		t.Fatalf("ran %v", tools.calls)
	}

	for i, call := range want {
		if tools.calls[i] != call {
			t.Errorf("operation %d ran %q, want %q", i, tools.calls[i], call)
		}
	}
}

func TestActionStopsAtTheFirstFailure(t *testing.T) {
	run := testRun(t, Minor)
	tools := &fakeTools{
		anyCommand: true,
		stderr:     map[string]string{"git fetch origin": "could not read from remote"},
	}
	run.Tools = tools

	err := RunAction(context.Background(), twoCommands(), run, strings.NewReader("y\n"), io.Discard)
	if err == nil {
		t.Fatal("a failed operation was reported as success")
	}

	// The push would otherwise tag a commit the fetch never brought over.
	if len(tools.calls) != 1 {
		t.Errorf("ran %v after the failure", tools.calls[1:])
	}
}

func TestActionRefusesAStepThatIsNotAvailable(t *testing.T) {
	step := twoCommands()
	step.Check = func(context.Context, *Run) (Answer, error) {
		return Answer{OK: true, Detail: "already tagged"}, nil
	}

	run := testRun(t, Minor)
	tools := &fakeTools{anyCommand: true}
	run.Tools = tools

	err := RunAction(context.Background(), step, run, strings.NewReader("y\n"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "done") {
		t.Fatalf("running a step that is done gave %v", err)
	}

	if len(tools.calls) != 0 {
		t.Errorf("a step that is done ran %v", tools.calls)
	}
}

func TestArgumentsWithSpacesAreShownQuoted(t *testing.T) {
	operation := command("", "git", "commit", "--message", "Bump version to 1.25.0")
	if want := `git commit --message "Bump version to 1.25.0"`; operation.Description != want {
		t.Errorf("shown as %s", operation.Description)
	}
}

func TestWhatAnOperationPrintsReachesTheTerminal(t *testing.T) {
	run := testRun(t, Minor)
	run.Tools = &fakeTools{
		anyCommand: true,
		output:     map[string]string{"git fetch origin": "counting objects\n"},
	}

	var out strings.Builder
	if err := RunAction(context.Background(), twoCommands(), run, strings.NewReader("y\n"), &out); err != nil {
		t.Fatal(err)
	}

	// A terminal showing nothing during a long operation looks like a tool
	// that has stopped.
	if !strings.Contains(out.String(), "counting objects") {
		t.Errorf("the command's output never reached the terminal:\n%s", out.String())
	}
}
