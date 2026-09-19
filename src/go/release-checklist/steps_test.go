// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"strings"
	"testing"
)

const testRepo = "me/rancher-desktop"

// checklistRun is a refresh against one release of a test repository, with
// push access and the refs the test gives it.
func checklistRun(t *testing.T, version Version, branches map[Line]string, tools *fakeTools) *Run {
	t.Helper()

	if tools.output == nil {
		tools.output = map[string]string{}
	}

	tools.output["gh api repos/"+testRepo+" --jq .permissions.push"] = "true\n"

	repo := &repository{repo: testRepo, url: "https://github.com/" + testRepo + ".git", run: tools}

	run := newRun(&Release{Version: version, Kind: version.Kind()},
		&Profile{Name: "test", GitHub: GitHubResources{Repo: testRepo}}, repo)
	run.Tools = tools
	run.Refs = &Refs{Branches: branches, Tags: map[Version]string{}}

	return run
}

func TestVersionBumpIsDoneWhenTheBranchCarriesTheVersion(t *testing.T) {
	version := Version{Major: 1, Minor: 25}
	tools := &fakeTools{output: map[string]string{
		"gh api repos/" + testRepo + "/contents/package.json?ref=release-1.25 " +
			"--header Accept: application/vnd.github.raw": strings.Replace(manifest, "1.24.0", "1.25.0", 1),
	}}

	run := checklistRun(t, version, map[Line]string{{Major: 1, Minor: 25}: "beef"}, tools)

	if status := run.Status(context.Background(), versionBump); status.State != Done {
		t.Errorf("the bump was %s: %s", status.State, status.Detail)
	}
}

func TestVersionBumpWaitsForTheReleaseBranch(t *testing.T) {
	version := Version{Major: 1, Minor: 25}
	tools := &fakeTools{stderr: map[string]string{
		"gh api repos/" + testRepo + "/contents/package.json?ref=release-1.25 " +
			"--header Accept: application/vnd.github.raw": "gh: No commit found for the ref release-1.25 (HTTP 404)",
	}}

	// A minor with no release branch: step 1 is available, so the bump has
	// nothing to bump.
	run := checklistRun(t, version, map[Line]string{}, tools)

	status := run.Status(context.Background(), versionBump)
	if status.State != Blocked {
		t.Fatalf("the bump was %s: %s", status.State, status.Detail)
	}

	if status.Detail == "" {
		t.Error("a blocked step said nothing about what it waits for")
	}
}

func TestPatchSkipsTheReleaseBranchAndStillBumps(t *testing.T) {
	version := Version{Major: 1, Minor: 24, Patch: 1}
	tools := &fakeTools{output: map[string]string{
		"gh api repos/" + testRepo + "/contents/package.json?ref=release-1.24 " +
			"--header Accept: application/vnd.github.raw": manifest,
		"gh api user --jq .login":                                                                "me\n",
		"gh api repos/me/rancher-desktop --jq .parent.full_name":                                 "someone/else\n",
		"gh api repos/" + testRepo + "/pulls?state=open&head=me:bump-to-1.24.1 --jq .[0].number": "",
	}}

	run := checklistRun(t, version, map[Line]string{{Major: 1, Minor: 24}: "beef"}, tools)

	// Step 1 is skipped for a patch, and a skipped step satisfies what
	// follows it, so the bump is available rather than blocked.
	if status := run.Status(context.Background(), releaseBranch); status.State != Skipped {
		t.Errorf("the release branch step was %s on a patch", status.State)
	}

	if status := run.Status(context.Background(), versionBump); status.State != Available {
		t.Errorf("the bump was %s: %s", status.State, status.Detail)
	}
}
