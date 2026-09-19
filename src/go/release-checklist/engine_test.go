// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
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

func TestPlaceholdersAreFilledWithTheReleasesOwnValues(t *testing.T) {
	run := testRun(t, Minor)
	run.Profile.GitHub.Repo = "me/rancher-desktop"

	got := fill("{version} {tag} {branch} {line} {repo}", run)
	if want := "1.25.0 v1.25.0 release-1.25 1.25 me/rancher-desktop"; got != want {
		t.Errorf("filled to %q", got)
	}
}
