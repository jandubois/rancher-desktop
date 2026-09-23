// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"strings"
	"testing"
)

// fakeRepo answers the detection's questions from a table, so each rule can
// be driven without reaching GitHub.
type fakeRepo struct {
	refs     *Refs
	releases map[string]ReleaseState
	inMain   map[string]bool
}

func (f *fakeRepo) Refs(context.Context) (*Refs, error) { return f.refs, nil }

func (f *fakeRepo) ReleaseState(_ context.Context, tag string) (ReleaseState, error) {
	if state, ok := f.releases[tag]; ok {
		return state, nil
	}

	return ReleaseMissing, nil
}

func (f *fakeRepo) TagInMain(_ context.Context, tag string) (bool, error) {
	return f.inMain[tag], nil
}

// branched adds a release line to the captured refs, at its own head and
// optionally at a tag, and returns facts answering for it.
func branched(t *testing.T, line Line, tags map[Version]string, head string) *fakeRepo {
	t.Helper()

	refs := upstreamRefs(t)
	refs.Branches[line] = head

	for version, commit := range tags {
		refs.Tags[version] = commit
	}

	return &fakeRepo{
		refs:     refs,
		releases: map[string]ReleaseState{},
		inMain:   map[string]bool{},
	}
}

func detect(t *testing.T, facts repoFacts) *Release {
	t.Helper()

	release, err := Detect(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}

	return release
}

func TestDetectTakesTheNextMinorWhenTheLastReleaseIsDone(t *testing.T) {
	// The repository as captured: release-1.24 is at v1.24.0's commit, and
	// v1.24.0 is published and merged back.
	facts := &fakeRepo{
		refs:     upstreamRefs(t),
		releases: map[string]ReleaseState{"v1.24.0": ReleasePublished},
		inMain:   map[string]bool{"v1.24.0": true},
	}

	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25}) || release.Kind != Minor {
		t.Errorf("drove %s, a %s release", release.Version, release.Kind)
	}
}

func TestDetectTakesTheBranchsFirstReleaseWhenItHasNoTag(t *testing.T) {
	// A new minor branch carries no tag of its own. The nearest tag in its
	// history belongs to the previous line, and taking that one would drive
	// a patch of the release before it.
	facts := branched(t, Line{Major: 1, Minor: 25}, nil, "beef")

	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25}) || release.Kind != Minor {
		t.Errorf("drove %s, a %s release", release.Version, release.Kind)
	}
}

func TestDetectStaysOnATaggedReleaseUntilItIsPublished(t *testing.T) {
	line := Line{Major: 1, Minor: 25}
	tagged := Version{Major: 1, Minor: 25}

	for _, state := range []ReleaseState{ReleaseDraft, ReleaseMissing} {
		facts := branched(t, line, map[Version]string{tagged: "beef"}, "beef")
		facts.releases["v1.25.0"] = state

		release := detect(t, facts)
		if release.Version != tagged || release.Published {
			t.Errorf("with a %s release, drove %s (published %v)", state, release.Version, release.Published)
		}
	}
}

func TestDetectWarnsAboutCommitsPastAnUnpublishedTag(t *testing.T) {
	facts := branched(t, Line{Major: 1, Minor: 25},
		map[Version]string{{Major: 1, Minor: 25}: "beef"}, "cafe")
	facts.releases["v1.25.0"] = ReleaseDraft

	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25}) {
		t.Fatalf("drove %s", release.Version)
	}

	if len(release.Warnings) != 1 || !strings.Contains(release.Warnings[0], "1.25.1") {
		t.Errorf("warnings were %q", release.Warnings)
	}
}

func TestDetectStaysOnAPublishedReleaseUntilItReachesMain(t *testing.T) {
	facts := branched(t, Line{Major: 1, Minor: 25},
		map[Version]string{{Major: 1, Minor: 25}: "beef"}, "beef")
	facts.releases["v1.25.0"] = ReleasePublished

	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25}) || !release.Published {
		t.Errorf("drove %s (published %v)", release.Version, release.Published)
	}
}

func TestDetectTakesTheNextPatchForCommitsPastAFinishedRelease(t *testing.T) {
	facts := branched(t, Line{Major: 1, Minor: 25},
		map[Version]string{{Major: 1, Minor: 25}: "beef"}, "cafe")
	facts.releases["v1.25.0"] = ReleasePublished
	facts.inMain["v1.25.0"] = true

	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25, Patch: 1}) || release.Kind != Patch {
		t.Errorf("drove %s, a %s release", release.Version, release.Kind)
	}
}

func TestDetectPassesOverBurnedVersions(t *testing.T) {
	facts := branched(t, Line{Major: 1, Minor: 25}, nil, "beef")
	facts.releases["burned-v1.25.0"] = ReleaseDraft

	// With 1.25.0 burned, 1.25.1 is the line's first release.
	release := detect(t, facts)
	if release.Version != (Version{Major: 1, Minor: 25, Patch: 1}) || release.Kind != Minor {
		t.Errorf("drove %s as a %s release with 1.25.0 burned", release.Version, release.Kind)
	}
}

func TestAVersionOverrideWhoseTagIsInMainIsFinished(t *testing.T) {
	facts := &fakeRepo{refs: upstreamRefs(t), inMain: map[string]bool{"v1.24.0": true}}

	t.Setenv("VERSION", "1.24.0")

	release, err := chooseRelease(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}

	if !release.Finished {
		t.Error("a release whose tag is in main is not finished")
	}

	t.Setenv("VERSION", "1.24.1")

	if release, err = chooseRelease(context.Background(), facts); err != nil {
		t.Fatal(err)
	}

	if release.Finished {
		t.Error("a release whose tag is not in main is finished")
	}
}

func TestAVersionOverrideIsAPatchOnceItsLineHasATag(t *testing.T) {
	facts := branched(t, Line{Major: 1, Minor: 25}, nil, "beef")

	t.Setenv("VERSION", "1.25.1")

	// With 1.25.0 burned, the line has no tag, so 1.25.1 opens it.
	release, err := chooseRelease(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}

	if release.Kind != Minor {
		t.Errorf("1.25.1 with no tag on its line was a %s release", release.Kind)
	}

	facts.refs.Tags[Version{Major: 1, Minor: 25}] = "beef"

	if release, err = chooseRelease(context.Background(), facts); err != nil {
		t.Fatal(err)
	}

	if release.Kind != Patch {
		t.Errorf("1.25.1 after v1.25.0 was a %s release", release.Kind)
	}
}
