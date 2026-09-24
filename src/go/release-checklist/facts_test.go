// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dependenciesYAML is dependencies.yaml cut to the shapes that matter: a
// version under a name, a quoted one, a tool no user reads the version of,
// and the guest ISO, whose version is a mapping rather than a string.
const dependenciesYAML = `ECRCredentialHelper:
  assets:
    - platform: linux
      url: https://example.invalid/0.12.0/docker-credential-ecr-login
  version: 0.12.0
WSLDistro:
  version: "0.99"
alpineLimaISO:
  version:
    alpineVersion: 3.24.1
    isoVersion: 0.2.47.rd9
dockerCLI:
  version: 29.5.3
electron:
  version: 42.3.3
helm:
  version: 4.2.1
kuberlr:
  version: 0.7.0
`

// bumpedDependencies is the same file after a release bumped two of the
// tools and the version of Electron the build uses.
var bumpedDependencies = strings.NewReplacer(
	"version: 29.5.3", "version: 29.6.2",
	"version: 4.2.1", "version: 4.2.3",
	"version: 42.3.3", "version: 43.1.1",
).Replace(dependenciesYAML)

// generatedNotes is GitHub's drafted body, cut to the two shapes the parser
// tells apart: a merged pull request naming its author, and a first
// contribution under the heading of its own. The second pull request is
// titled to read as a first contribution, which only the heading it sits
// under tells apart from one.
const generatedNotes = `## What's Changed
* Fix a typo on the Troubleshooting page by @regular in https://github.com/me/rancher-desktop/pull/102
* @nobody made their first contribution in 2019 by @regular in https://github.com/me/rancher-desktop/pull/101

## New Contributors
* @newcomer made their first contribution in https://github.com/me/rancher-desktop/pull/103

**Full Changelog**: https://github.com/me/rancher-desktop/compare/v1.24.0...v1.25.0
`

// previousTagged is the release 1.25.0 is written against, and the date of
// its commit bounds the search for what a contributor did before it.
var (
	previousTagged = Version{Major: 1, Minor: 24}
	previousDate   = "2026-08-12T17:58:31Z"
)

func TestAVersionThatIsNotOneIsLeftOut(t *testing.T) {
	versions, err := dependencyVersions([]byte(dependenciesYAML))
	if err != nil {
		t.Fatal(err)
	}

	if version, has := versions["alpineLimaISO"]; has {
		t.Errorf("the guest ISO has no single version, but %q was read for it", version)
	}

	for name, want := range map[string]string{"dockerCLI": "29.5.3", "WSLDistro": "0.99"} {
		if got := versions[name]; got != want {
			t.Errorf("%s came out as %q, not %q", name, got, want)
		}
	}
}

func TestTheUtilitiesComeOutInTheOrderTheNotesUse(t *testing.T) {
	before := map[string]string{"helm": "4.2.1", "dockerCLI": "29.5.3", "trivy": "0.71.1", "kuberlr": "0.7.0"}
	after := map[string]string{"helm": "4.2.3", "dockerCLI": "29.6.2", "trivy": "0.71.1", "kuberlr": "0.7.0"}

	moved, same := utilityChanges(before, after)

	if want := []string{"* docker `29.5.3` → `29.6.2`", "* helm `4.2.1` → `4.2.3`"}; !slices.Equal(moved, want) {
		t.Errorf("the tools that moved came out as %q, not %q", moved, want)
	}

	if want := []string{"* kuberlr `0.7.0`", "* trivy `0.71.1`"}; !slices.Equal(same, want) {
		t.Errorf("the tools that stayed came out as %q, not %q", same, want)
	}
}

// TestABurnedVersionIsNotWrittenAgainst walks the releases a line made
// rather than the versions it used up, because a burned version shipped to
// nobody and its notes were never read.
func TestABurnedVersionIsNotWrittenAgainst(t *testing.T) {
	// The 1.24 line burned 1.24.0 and released 1.24.1, and the 1.23 line
	// burned 1.23.2 after releasing 1.23.1.
	refs := &Refs{Tags: map[Version]string{
		{Major: 1, Minor: 23}:           "aaa",
		{Major: 1, Minor: 23, Patch: 1}: "bbb",
		{Major: 1, Minor: 24, Patch: 1}: "ccc",
	}}

	for _, want := range []struct {
		release, previous Version
	}{
		{Version{Major: 1, Minor: 24}, Version{Major: 1, Minor: 23}},
		{Version{Major: 1, Minor: 24, Patch: 1}, Version{Major: 1, Minor: 23}},
		{Version{Major: 1, Minor: 25}, Version{Major: 1, Minor: 24, Patch: 1}},
		{Version{Major: 1, Minor: 23, Patch: 2}, Version{Major: 1, Minor: 23, Patch: 1}},
		{Version{Major: 1, Minor: 23, Patch: 3}, Version{Major: 1, Minor: 23, Patch: 1}},
	} {
		got, found := previousRelease(want.release, refs)
		if !found || got != want.previous {
			t.Errorf("%s is written against %s, not %s", want.release, got, want.previous)
		}
	}
}

func TestOnlyTheFirstContributionsAreRead(t *testing.T) {
	named := parseNewContributors(generatedNotes)

	if len(named) != 1 || named[0].Login != "newcomer" {
		t.Fatalf("the drafted notes gave %+v", named)
	}

	if !strings.HasSuffix(named[0].PR, "/103") {
		t.Errorf("the pull request came out as %q", named[0].PR)
	}
}

func TestSomebodyWhoContributedBeforeIsNotCredited(t *testing.T) {
	section := contributorSection([]contributor{
		{Login: "newcomer", PR: "https://example.invalid/103"},
		{Login: "olddog", PR: "https://example.invalid/10600", Earlier: true},
	}, previousTagged)

	if !strings.Contains(section, "Thank you to our new contributor: @newcomer!") {
		t.Errorf("the section credits nobody:\n%s", section)
	}

	if strings.Contains(section, "@olddog!") || strings.Contains(section, "@olddog, ") {
		t.Errorf("the section credits somebody who contributed before:\n%s", section)
	}

	if !strings.Contains(section, "@olddog already had a commit before v1.24.0") {
		t.Errorf("the section does not say why @olddog was left out:\n%s", section)
	}
}

// factsAnswers is every command the notes facts ask, answered the way the
// real tools answer it.
func factsAnswers() map[string]string {
	return map[string]string{
		dependenciesQuery(previousTagged.Tag()): dependenciesYAML,
		dependenciesQuery(testBranch):           bumpedDependencies,
		"gh api --method POST repos/" + testRepo + "/releases/generate-notes " +
			"--field tag_name=v1.25.0 --field previous_tag_name=v1.24.0 " +
			"--field target_commitish=" + testBranch + " --jq .body": generatedNotes,
		"gh api repos/" + testRepo + "/commits/v1.24.0 --jq .commit.committer.date": previousDate + "\n",
		"gh api repos/" + testRepo + "/commits?author=newcomer&until=" + previousDate +
			"&per_page=1 --jq length": "0\n",
		"gh api --paginate repos/" + testRepo + "/milestones?state=all&per_page=100 " +
			"--jq .[] | select(.title == \"1.25\") | .number": "61\n",
	}
}

// dependenciesQuery is the command that reads dependencies.yaml at a ref.
func dependenciesQuery(ref string) string {
	return "gh api repos/" + testRepo + "/contents/" + dependenciesFile + "?ref=" + ref +
		" --header Accept: application/vnd.github.raw"
}

// factsRun is a refresh of a release whose previous one is tagged, against a
// repository that answers everything the facts ask.
func factsRun(t *testing.T, answers map[string]string) *Run {
	t.Helper()

	run := checklistRun(t, testRelease, map[Line]string{testRelease.Line(): testHead},
		&fakeTools{output: answers})
	run.Refs.Tags = map[Version]string{previousTagged: "aaa"}

	return run
}

func TestTheNotesFactsArePastedStraightIntoTheNotes(t *testing.T) {
	facts, err := gatherNotesFacts(t.Context(), factsRun(t, factsAnswers()))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"## Updates to Bundled Utilities (from Rancher Desktop 1.24.0)",
		"* docker `29.5.3` → `29.6.2`",
		"* helm `4.2.1` → `4.2.3`",
		"Unchanged:\n* kuberlr `0.7.0`",
		"Thank you to our new contributor: @newcomer!",
		"compare/v1.24.0...v1.25.0",
		"milestone/61?closed=1",
	} {
		if !strings.Contains(facts, want) {
			t.Errorf("the facts do not hold %q:\n%s", want, facts)
		}
	}

	// Electron moved too, but it builds Rancher Desktop rather than shipping
	// beside it, so no user reads its version in the notes.
	if strings.Contains(facts, "electron") || strings.Contains(facts, "43.1.1") {
		t.Errorf("the facts name a tool the notes leave out:\n%s", facts)
	}
}

func TestTheChangelogSaysWhenThereIsNoMilestoneToLink(t *testing.T) {
	answers := factsAnswers()
	answers["gh api --paginate repos/"+testRepo+"/milestones?state=all&per_page=100 "+
		"--jq .[] | select(.title == \"1.25\") | .number"] = "\n"

	facts, err := gatherNotesFacts(t.Context(), factsRun(t, answers))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(facts, `no milestone called "1.25" yet`) {
		t.Errorf("the changelog does not say the milestone is missing:\n%s", facts)
	}
}

func TestALinesFirstReleaseAfterABurnedOneLinksTheLinesMilestone(t *testing.T) {
	run := factsRun(t, factsAnswers())
	run.Release = &Release{Version: Version{Major: 1, Minor: 25, Patch: 1}, Kind: Minor}

	changelog, err := changelogSection(t.Context(), run, previousTagged)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(changelog, "milestone/61?closed=1") {
		t.Errorf("the changelog does not link the 1.25 milestone:\n%s", changelog)
	}
}

func TestGatheringWritesUnderTheReleasesCacheDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	run := checklistRun(t, testRelease, nil, &fakeTools{})
	step := &Step{ID: "6", Facts: &Facts{File: factsFile,
		Gather: func(context.Context, *Run) (string, error) { return "the facts\n", nil }}}

	path, err := GatherFacts(t.Context(), step, run)
	if err != nil {
		t.Fatal(err)
	}

	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(cache, factsFile); path != want {
		t.Errorf("the facts went to %s, not %s", path, want)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(written) != "the facts\n" {
		t.Errorf("the file holds %q", written)
	}
}

func TestAStepWithNoFactsGathersNone(t *testing.T) {
	run := checklistRun(t, testRelease, nil, &fakeTools{})

	if _, err := GatherFacts(t.Context(), releaseBranch, run); err == nil {
		t.Error("a step that gathers nothing wrote a file anyway")
	}
}
