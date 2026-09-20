// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// theNotes is what a release's notes say in these tests.
const theNotes = "# Rancher Desktop 1.25.0\n\nThe interface speaks eight more languages.\n"

// releaseJSON is what gh answers for a draft release carrying these notes.
func releaseJSON(t *testing.T, notes string) string {
	t.Helper()

	answer, err := json.Marshal(struct {
		IsDraft bool   `json:"isDraft"`
		Body    string `json:"body"`
	}{IsDraft: true, Body: notes})
	if err != nil {
		t.Fatal(err)
	}

	return string(answer)
}

// notesRun is a refresh against a release whose draft carries the notes it is
// given, from a clone holding the release-notes.md it is given. A clone with
// no notes yet passes an empty file name.
func notesRun(t *testing.T, published, written string) *Run {
	t.Helper()

	clone := t.TempDir()

	if written != "" {
		if err := os.WriteFile(filepath.Join(clone, notesFile), []byte(written), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tools := &fakeTools{output: map[string]string{
		"git rev-parse --show-toplevel":                                           clone + "\n",
		"gh release view v1.25.0 --repo " + testRepo + " --json " + releaseFields: releaseJSON(t, published),
	}}

	return checklistRun(t, testRelease, map[Line]string{{Major: 1, Minor: 25}: testHead}, tools)
}

func TestReleaseNotesWaitForTheDraftRelease(t *testing.T) {
	tools := &fakeTools{stderr: map[string]string{
		"gh release view v1.25.0 --repo " + testRepo + " --json " + releaseFields: "release not found",
	}}

	run := checklistRun(t, testRelease, map[Line]string{{Major: 1, Minor: 25}: testHead}, tools)

	status := run.Status(context.Background(), releaseNotes)
	if status.State != Blocked {
		t.Fatalf("the notes step was %s with no draft: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "Draft release") {
		t.Errorf("the detail %q does not name the step it waits for", status.Detail)
	}
}

func TestReleaseNotesStayAvailableUntilSomebodyMarksThemDone(t *testing.T) {
	run := notesRun(t, theNotes, theNotes)

	status := run.Status(context.Background(), releaseNotes)
	if status.State != Available {
		t.Fatalf("unmarked notes were %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "marked") {
		t.Errorf("the detail %q does not say the notes need marking", status.Detail)
	}
}

func TestMarkedReleaseNotesAreDone(t *testing.T) {
	marking := notesRun(t, theNotes, theNotes)

	if err := Mark(context.Background(), releaseNotes, marking); err != nil {
		t.Fatal(err)
	}

	// A second refresh reads the release again, the way the dashboard does
	// after a key press.
	reading := notesRun(t, theNotes, theNotes)
	reading.Confirmations = marking.Confirmations

	if status := reading.Status(context.Background(), releaseNotes); status.State != Done {
		t.Errorf("marked notes were %s: %s", status.State, status.Detail)
	}
}

func TestNotesEditedAfterTheMarkNeedMarkingAgain(t *testing.T) {
	marking := notesRun(t, theNotes, theNotes)

	if err := Mark(context.Background(), releaseNotes, marking); err != nil {
		t.Fatal(err)
	}

	edited := theNotes + "\nAnd it fixes the updater.\n"

	reading := notesRun(t, edited, edited)
	reading.Confirmations = marking.Confirmations

	status := reading.Status(context.Background(), releaseNotes)
	if status.State != Available {
		t.Fatalf("notes edited after the mark were %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "changed") {
		t.Errorf("the detail %q does not say the notes changed", status.Detail)
	}
}

func TestMarkingTwiceTakesTheMarkOff(t *testing.T) {
	run := notesRun(t, theNotes, theNotes)

	for range 2 {
		if err := Mark(context.Background(), releaseNotes, run); err != nil {
			t.Fatal(err)
		}
	}

	if confirmation, marked := run.Confirmation(releaseNotes.ID); marked {
		t.Errorf("marking twice left the step marked on %s", markedOn(confirmation))
	}
}

func TestAStepSettledByItsCheckCannotBeMarked(t *testing.T) {
	run := notesRun(t, theNotes, theNotes)

	err := Mark(context.Background(), tagRelease, run)
	if err == nil || !strings.Contains(err.Error(), "nothing to mark") {
		t.Errorf("marking the tag step gave %v", err)
	}
}

func TestNotesThatDifferFromTheCloneAreNotDone(t *testing.T) {
	run := notesRun(t, theNotes, theNotes+"\nA line the release has not seen.\n")

	status := run.Status(context.Background(), releaseNotes)
	if status.State != Available {
		t.Fatalf("notes differing from the clone were %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, notesFile) {
		t.Errorf("the detail %q does not name the file that differs", status.Detail)
	}
}

func TestLineEndingsDoNotMakeTheNotesDiffer(t *testing.T) {
	// GitHub gives a release body back with CRLF line endings, whatever was
	// uploaded, so the bytes differ where the text does not.
	run := notesRun(t, strings.ReplaceAll(theNotes, "\n", "\r\n"), theNotes)

	if err := Mark(context.Background(), releaseNotes, run); err != nil {
		t.Fatal(err)
	}

	reading := notesRun(t, strings.ReplaceAll(theNotes, "\n", "\r\n"), theNotes)
	reading.Confirmations = run.Confirmations

	if status := reading.Status(context.Background(), releaseNotes); status.State != Done {
		t.Errorf("notes that only differ in line endings were %s: %s", status.State, status.Detail)
	}
}

func TestTheNotesActionUploadsTheClonesCopy(t *testing.T) {
	run := notesRun(t, "", theNotes)

	operations, err := planReleaseNotes(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if len(operations) != 1 {
		t.Fatalf("the action planned %d operations", len(operations))
	}

	if !strings.HasPrefix(operations[0].Description, "gh release edit v1.25.0 --repo "+testRepo) {
		t.Errorf("the action runs %q", operations[0].Description)
	}

	if !strings.HasSuffix(operations[0].Description, notesFile) {
		t.Errorf("the action does not upload %s: %q", notesFile, operations[0].Description)
	}
}

func TestTheNotesActionRefusesWhenTheReleaseAlreadyHasThem(t *testing.T) {
	run := notesRun(t, theNotes, theNotes)

	_, err := planReleaseNotes(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("planning an upload of notes already published gave %v", err)
	}
}

func TestTheNotesActionRefusesWhenTheCloneHasNoNotes(t *testing.T) {
	run := notesRun(t, theNotes, "")

	_, err := planReleaseNotes(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), notesFile) {
		t.Errorf("planning an upload from a clone without notes gave %v", err)
	}
}

func TestTheNotesActionShowsWhatItWouldChange(t *testing.T) {
	// The release's own notes are written under the cache directory for git
	// to compare, so the cache goes somewhere the test can throw away.
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)

	run := notesRun(t, theNotes, theNotes+"\nA new line.\n")
	tools, _ := run.Tools.(*fakeTools)
	tools.anyCommand = true

	summary, err := notesChange(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	diffed := tools.calls[len(tools.calls)-1]
	if !strings.HasPrefix(diffed, "git diff --no-index -- ") {
		t.Errorf("the summary came from %q", diffed)
	}

	if !strings.Contains(diffed, draftNotesFile) || !strings.Contains(diffed, notesFile) {
		t.Errorf("the diff compares %q", diffed)
	}

	if summary != "" {
		t.Errorf("the fake git printed %q", summary)
	}
}

func TestAConfirmationOutlivesTheStoreThatWroteIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), confirmationsFile)

	writing, err := confirmationsAt(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := writing.Confirm(testRelease, releaseNotes.ID, "a digest"); err != nil {
		t.Fatal(err)
	}

	reading, err := confirmationsAt(path)
	if err != nil {
		t.Fatal(err)
	}

	confirmation, marked := reading.Confirmed(testRelease, releaseNotes.ID)
	if !marked || confirmation.Digest != "a digest" {
		t.Fatalf("the reopened store answered %+v, %v", confirmation, marked)
	}

	if confirmation.At.IsZero() {
		t.Error("the confirmation does not say when it was made")
	}

	if err := reading.Unconfirm(testRelease, releaseNotes.ID); err != nil {
		t.Fatal(err)
	}

	if _, marked := reading.Confirmed(testRelease, releaseNotes.ID); marked {
		t.Error("the step is still marked after the mark came off")
	}
}

func TestAProfileThatHasMarkedNothingHasNoFile(t *testing.T) {
	store, err := confirmationsAt(filepath.Join(t.TempDir(), confirmationsFile))
	if err != nil {
		t.Fatal(err)
	}

	if _, marked := store.Confirmed(testRelease, releaseNotes.ID); marked {
		t.Error("a store with no file behind it answered that a step is marked")
	}
}
