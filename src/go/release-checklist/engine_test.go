// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// countingResource answers as told and counts how often it is probed.
type countingResource struct {
	resource *Resource
	probes   int
}

func newCountingResource(answer Answer, err error) *countingResource {
	counted := &countingResource{}
	counted.resource = &Resource{
		Name:       "test resource",
		Command:    "git",
		Install:    "https://git-scm.com",
		Configured: func(p *Profile) bool { return p.OBS != nil },
		Probe: func(context.Context, *Run) (Answer, error) {
			counted.probes++

			return answer, err
		},
	}

	return counted
}

func testRun(t *testing.T, kind Kind) *Run {
	t.Helper()

	version := Version{Major: 1, Minor: 25}
	if kind == Patch {
		version.Patch = 1
	}

	return &Run{
		Release: &Release{Version: version, Kind: kind},
		Profile: &Profile{Name: "test", OBS: &OBSResources{DevProject: "home:me"}},
		Tools:   &fakeTools{},
	}
}

// answering is a step whose check and precondition report what the test says.
func answering(check, precondition Answer, kinds []Kind, needs []*Resource) *Step {
	step := &Step{ID: "1", Title: "Test step", Kinds: kinds, Needs: needs}
	step.Check = func(context.Context, *Run) (Answer, error) { return check, nil }
	step.Precondition = func(context.Context, *Run) (Answer, error) { return precondition, nil }

	return step
}

func TestStepIsSkippedForTheOtherKindOfRelease(t *testing.T) {
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, nil)

	status := Evaluate(context.Background(), step, testRun(t, Patch))
	if status.State != Skipped {
		t.Errorf("a minor-only step on a patch was %s: %s", status.State, status.Detail)
	}
}

func TestStepIsSkippedWhenTheProfileLeavesItsSystemOut(t *testing.T) {
	counted := newCountingResource(Answer{OK: true}, nil)
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, []*Resource{counted.resource})

	run := testRun(t, Minor)
	run.Profile.OBS = nil

	status := Evaluate(context.Background(), step, run)
	if status.State != Skipped {
		t.Errorf("a step whose system the profile leaves out was %s: %s", status.State, status.Detail)
	}

	if counted.probes != 0 {
		t.Errorf("a skipped step probed its resource %d times", counted.probes)
	}
}

func TestMissingToolBlocksTheStepAndNamesTheFix(t *testing.T) {
	counted := newCountingResource(Answer{OK: true}, nil)
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, []*Resource{counted.resource})

	run := testRun(t, Minor)
	run.Tools = &fakeTools{absent: map[string]bool{"git": true}}

	status := Evaluate(context.Background(), step, run)
	if status.State != Blocked {
		t.Fatalf("a step with no git was %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "https://git-scm.com") {
		t.Errorf("the detail %q does not say where to get git", status.Detail)
	}
}

func TestAProbeThatCannotDecideLeavesTheStepUnknown(t *testing.T) {
	counted := newCountingResource(Answer{}, errors.New("the network is down"))
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, []*Resource{counted.resource})

	status := Evaluate(context.Background(), step, testRun(t, Minor))
	if status.State != Unknown {
		t.Errorf("a step whose probe failed was %s: %s", status.State, status.Detail)
	}
}

func TestDeniedAccessBlocksTheStep(t *testing.T) {
	counted := newCountingResource(Answer{Detail: "no push access"}, nil)
	step := answering(Answer{}, Answer{OK: true}, []Kind{Minor}, []*Resource{counted.resource})

	status := Evaluate(context.Background(), step, testRun(t, Minor))
	if status.State != Blocked || status.Detail != "no push access" {
		t.Errorf("a step without access was %s: %s", status.State, status.Detail)
	}
}

func TestAnActionIsBlockedWhereItCannotWrite(t *testing.T) {
	denied := newCountingResource(Answer{Detail: "no push access"}, nil)

	undone := answering(Answer{Detail: "not there"}, Answer{OK: true}, []Kind{Minor}, nil)
	undone.Action = &Action{Writes: []*Resource{denied.resource}}

	status := Evaluate(context.Background(), undone, testRun(t, Minor))
	if status.State != Blocked || status.Detail != "no push access" {
		t.Errorf("a step whose action cannot write was %s: %s", status.State, status.Detail)
	}

	done := answering(Answer{OK: true}, Answer{OK: true}, []Kind{Minor}, nil)
	done.Action = &Action{Writes: []*Resource{denied.resource}}

	if status := Evaluate(context.Background(), done, testRun(t, Minor)); status.State != Done {
		t.Errorf("done work whose action cannot write was %s: %s", status.State, status.Detail)
	}
}

func TestEveryActionNamesWhatItWrites(t *testing.T) {
	for _, step := range checklist {
		if step.Action != nil && len(step.Action.Writes) == 0 {
			t.Errorf("the action of step %s names nothing it writes", step.ID)
		}
	}
}

func TestCheckAndPreconditionDecideTheRemainingStates(t *testing.T) {
	cases := map[State]*Step{
		Done:      answering(Answer{OK: true, Detail: "already there"}, Answer{}, []Kind{Minor}, nil),
		Available: answering(Answer{Detail: "not there"}, Answer{OK: true}, []Kind{Minor}, nil),
		Waiting:   answering(Answer{}, Answer{Waiting: true, Detail: "the build is running"}, []Kind{Minor}, nil),
		Blocked:   answering(Answer{}, Answer{Detail: "step 4 is not done"}, []Kind{Minor}, nil),
	}

	for want, step := range cases {
		if status := Evaluate(context.Background(), step, testRun(t, Minor)); status.State != want {
			t.Errorf("wanted %s, got %s: %s", want, status.State, status.Detail)
		}
	}
}

// judgedStep is a step whose check has passed and whose work takes a
// person's judgment, with a precondition the test controls.
func judgedStep(precondition Answer) *Step {
	step := answering(Answer{OK: true, Detail: "the page reports 1.25.0"}, precondition, []Kind{Minor}, nil)
	step.Confirms = func(context.Context, *Run) (string, error) { return "the page", nil }

	return step
}

func TestAJudgedStepWaitsForItsPreconditionBeforeItIsAvailable(t *testing.T) {
	run := testRun(t, Minor)

	step := judgedStep(Answer{Detail: "this machine holds snapshots"})
	if status := Evaluate(context.Background(), step, run); status.State != Blocked {
		t.Errorf("a judged step whose precondition fails is %s: %s", status.State, status.Detail)
	}

	step = judgedStep(Answer{OK: true})
	if status := Evaluate(context.Background(), step, run); status.State != Available {
		t.Errorf("a judged step whose precondition holds is %s: %s", status.State, status.Detail)
	}

	// A mark settles the step whatever the machine looks like afterwards.
	marked, err := confirmationsAt(filepath.Join(t.TempDir(), confirmationsFile))
	if err != nil {
		t.Fatal(err)
	}

	run.Confirmations = marked
	step = judgedStep(Answer{Detail: "this machine holds snapshots"})

	if err := Mark(context.Background(), step, run); err != nil {
		t.Fatal(err)
	}

	if status := Evaluate(context.Background(), step, run); status.State != Done {
		t.Errorf("a marked step is %s: %s", status.State, status.Detail)
	}
}

func TestAStepWaitsWhileTheStepItFollowsWaits(t *testing.T) {
	first := answering(Answer{}, Answer{Waiting: true, Detail: "the build is still running"}, []Kind{Minor}, nil)

	second := answering(Answer{}, Answer{}, []Kind{Minor}, nil)
	second.ID = "2"
	second.Precondition = func(ctx context.Context, run *Run) (Answer, error) {
		return waitFor(ctx, run, first), nil
	}

	if status := testRun(t, Minor).Status(context.Background(), second); status.State != Waiting {
		t.Errorf("a step following a waiting step is %s: %s", status.State, status.Detail)
	}
}

func TestAMarkOnTheMinorReleaseLeavesItsPatchUnmarked(t *testing.T) {
	marked, err := confirmationsAt(filepath.Join(t.TempDir(), confirmationsFile))
	if err != nil {
		t.Fatal(err)
	}

	step := judgedStep(Answer{OK: true})
	step.Kinds = []Kind{Minor, Patch}

	minor := testRun(t, Minor)
	minor.Confirmations = marked

	if err := Mark(context.Background(), step, minor); err != nil {
		t.Fatal(err)
	}

	patch := testRun(t, Patch)
	patch.Confirmations = marked

	if status := Evaluate(context.Background(), step, patch); status.State == Done {
		t.Errorf("a step marked on 1.25.0 is %s on 1.25.1: %s", status.State, status.Detail)
	}
}

func TestMarkRefusesAStepWithNothingToMark(t *testing.T) {
	marked, err := confirmationsAt(filepath.Join(t.TempDir(), confirmationsFile))
	if err != nil {
		t.Fatal(err)
	}

	run := testRun(t, Minor)
	run.Confirmations = marked

	step := judgedStep(Answer{OK: true})
	step.Confirms = func(context.Context, *Run) (string, error) { return "", nil }

	if err := Mark(context.Background(), step, run); err == nil {
		t.Error("marking a step with no text to mark succeeded")
	}

	if _, found := run.Confirmation(step.ID); found {
		t.Error("a step with no text to mark was marked")
	}
}

func TestOneResourceIsProbedOncePerRefresh(t *testing.T) {
	counted := newCountingResource(Answer{OK: true}, nil)
	needs := []*Resource{counted.resource}

	run := testRun(t, Minor)
	for range 3 {
		Evaluate(context.Background(), answering(Answer{OK: true}, Answer{}, []Kind{Minor}, needs), run)
	}

	if counted.probes != 1 {
		t.Errorf("three steps needing one resource probed it %d times", counted.probes)
	}
}

func TestARefreshAsksGitHubEachQuestionOnce(t *testing.T) {
	page := strings.Replace(string(documentedPage(t)), "v1.24.0,", testRelease.Tag()+",", 1)

	for _, tagged := range []bool{false, true} {
		inACacheOfItsOwn(t)

		answers := readyToTag(packageRunJSON("completed", "success"))
		maps.Copy(answers, docsAnswers())
		maps.Copy(answers, referenceAnswers(page))
		answers["gh release view v1.25.0 --repo "+testRepo+" --json "+releaseFields] =
			`{"isDraft":true,"body":"some notes","assets":[]}`
		answers["git rev-parse --show-toplevel"] = t.TempDir() + "\n"

		if !tagged {
			answers["gh api repos/"+testRepo+" --jq .parent.full_name"] = "someone/else\n"
		} else {
			answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
			answers[runsQuery("v1.25.0")] = packageRunJSON("completed", "success")
			answers[artifactsQuery()] = `{"total_count":2,"artifacts":[` +
				`{"name":"Rancher Desktop-linux.zip","expired":false,"size_in_bytes":1},` +
				`{"name":"Rancher Desktop-win.zip","expired":false,"size_in_bytes":1}]}`
			answers[fileQuery(testRepo, "v1.25.0", dependenciesFile)] = docsDependencies
		}

		tools := &fakeTools{output: answers, stderr: map[string]string{"rdctl list-settings": "not running"}}
		run := docsRun(t, tools)
		if tagged {
			run.Refs.Tags = map[Version]string{testRelease: testHead}
		}

		for _, step := range checklist {
			run.Status(context.Background(), step)
		}

		// Only gh calls ask GitHub; git and rdctl read this machine.
		counts := map[string]int{}
		for _, call := range tools.calls {
			if strings.HasPrefix(call, "gh ") {
				counts[call]++
			}
		}

		for _, call := range slices.Sorted(maps.Keys(counts)) {
			if count := counts[call]; count > 1 {
				t.Errorf("tagged %v: %q ran %d times", tagged, call, count)
			}
		}
	}
}

func TestPlaceholdersAreFilledWithTheReleasesOwnValues(t *testing.T) {
	run := testRun(t, Minor)
	run.Profile.GitHub.Repo = "me/rancher-desktop"
	run.Profile.GitHub.DocsRepo = "me/docs.rancherdesktop.io"

	got := fill("{version} {tag} {branch} {line} {repo} {docsRepo}", run)
	if want := "1.25.0 v1.25.0 release-1.25 1.25 me/rancher-desktop me/docs.rancherdesktop.io"; got != want {
		t.Errorf("filled to %q", got)
	}
}
