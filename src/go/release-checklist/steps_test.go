// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	testRepo   = "me/rancher-desktop"
	testBranch = "release-1.25"
	// testHead is the commit the release branch points at, which is what
	// the tag step tags.
	testHead = "beefbeefbeefbeefbeefbeefbeefbeefbeefbeef"
)

// testRelease is the release the tag and package-build tests drive.
var testRelease = Version{Major: 1, Minor: 25}

// checklistRun is a refresh against one release of a test repository, with
// push access and the refs the test gives it.
func checklistRun(t *testing.T, version Version, branches map[Line]string, tools *fakeTools) *Run {
	t.Helper()

	if tools.output == nil {
		tools.output = map[string]string{}
	}

	tools.output["gh api repos/"+testRepo+" --jq .permissions.push"] = "true\n"

	url := "https://github.com/" + testRepo + ".git"
	repo := &repository{repo: testRepo, url: url, pushURL: url, run: tools}

	marked, err := confirmationsAt(filepath.Join(t.TempDir(), confirmationsFile))
	if err != nil {
		t.Fatal(err)
	}

	tags := map[Version]string{}
	if version.Patch > 0 {
		// A patch follows the release its line opened with.
		tags[Version{Major: version.Major, Minor: version.Minor}] = "feed"
	}

	refs := &Refs{Branches: branches, Tags: tags}

	run := newRun(&Release{Version: version, Kind: refs.KindOf(version)},
		&Profile{Name: "test", GitHub: GitHubResources{Repo: testRepo}}, repo)
	run.Tools = tools
	run.Refs = refs
	run.Confirmations = marked

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
	run := checklistRun(t, version, map[Line]string{{Major: 1, Minor: 24}: "beef"},
		&fakeTools{output: bumpAnswers("")})

	// Step 1 is skipped for a patch, and a skipped step satisfies what
	// follows it, so the bump is available rather than blocked.
	if status := run.Status(context.Background(), releaseBranch); status.State != Skipped {
		t.Errorf("the release branch step was %s on a patch", status.State)
	}

	if status := run.Status(context.Background(), versionBump); status.State != Available {
		t.Errorf("the bump was %s: %s", status.State, status.Detail)
	}
}

// bumpAnswers is what the tools answer for the 1.24.1 bump while package.json
// on the release branch still says 1.24.0. pr is what the pull request lookup
// prints.
func bumpAnswers(pr string) map[string]string {
	return map[string]string{
		contentsQuery("release-1.24"):                            manifest,
		"gh api user --jq .login":                                "me\n",
		"gh api repos/me/rancher-desktop --jq .parent.full_name": "someone/else\n",
		"gh api repos/" + testRepo + "/pulls?state=open&head=me:bump-to-1.24.1 --jq .[0].number": pr,
	}
}

func TestVersionBumpWaitsForItsOpenPullRequest(t *testing.T) {
	version := Version{Major: 1, Minor: 24, Patch: 1}
	run := checklistRun(t, version, map[Line]string{{Major: 1, Minor: 24}: "beef"},
		&fakeTools{output: bumpAnswers("42\n")})

	status := run.Status(context.Background(), versionBump)
	if status.State != Waiting || !strings.Contains(status.Detail, "#42") {
		t.Errorf("the bump with its pull request open was %s: %s", status.State, status.Detail)
	}
}

func TestVersionBumpReportsAFailedPullRequestLookup(t *testing.T) {
	version := Version{Major: 1, Minor: 24, Patch: 1}
	answers := bumpAnswers("")
	query := "gh api repos/" + testRepo + "/pulls?state=open&head=me:bump-to-1.24.1 --jq .[0].number"
	delete(answers, query)

	tools := &fakeTools{output: answers, stderr: map[string]string{query: "gh: Server Error (HTTP 502)"}}
	run := checklistRun(t, version, map[Line]string{{Major: 1, Minor: 24}: "beef"}, tools)

	if status := run.Status(context.Background(), versionBump); status.State != Unknown {
		t.Errorf("the bump whose pull request lookup failed was %s: %s", status.State, status.Detail)
	}
}

// contentsQuery is the command that reads package.json at a ref.
func contentsQuery(ref string) string {
	return "gh api repos/" + testRepo + "/contents/package.json?ref=" + ref +
		" --header Accept: application/vnd.github.raw"
}

// runsQuery is the command that looks up the package run for a ref at the
// release branch head.
func runsQuery(ref string) string { return runsQueryAt(ref, testHead) }

// runsQueryAt is the command that looks up the package run for a ref at a
// commit.
func runsQueryAt(ref, commit string) string {
	return "gh api repos/" + testRepo + "/actions/workflows/package.yaml/runs?branch=" +
		ref + "&head_sha=" + commit + "&per_page=1"
}

// packageRunJSON is GitHub's answer for a workflow's runs, in the shape a
// real lookup returns.
func packageRunJSON(status, conclusion string) string {
	return fmt.Sprintf(`{"total_count":1,"workflow_runs":[{"id":30474476005,"status":%q,`+
		`"conclusion":%q,"html_url":"https://github.com/%s/actions/runs/30474476005"}]}`,
		status, conclusion, testRepo)
}

// readyToTag answers every command a release whose bump and draft are done
// asks, with its release branch head carrying the version, absent from main,
// and built by the package run the caller describes. A tag at that head is on
// the release branch.
func readyToTag(build string) map[string]string {
	return map[string]string{
		contentsQuery(testBranch): strings.Replace(manifest, "1.24.0", "1.25.0", 1),
		"gh release view v1.25.0 --repo " + testRepo + " --json " + releaseFields:       "{\"isDraft\":true}",
		"gh api repos/" + testRepo + "/compare/" + testHead + "...main --jq .behind_by": "2\n",
		runsQuery(testBranch): build,
		branchQuery(testHead): "0\n",
	}
}

// branchQuery is the command that counts the commits a commit has that the
// release branch does not.
func branchQuery(commit string) string {
	return "gh api repos/" + testRepo + "/compare/" + commit + "..." + testBranch + " --jq .behind_by"
}

// tagRun is a refresh of a release ready to be tagged, with the tags the
// test gives it.
func tagRun(t *testing.T, tools *fakeTools, tags map[Version]string) *Run {
	t.Helper()

	run := checklistRun(t, testRelease, map[Line]string{testRelease.Line(): testHead}, tools)
	run.Refs.Tags = tags

	return run
}

func TestTagIsDoneWhenItNamesACommitCarryingTheVersion(t *testing.T) {
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)

	run := tagRun(t, &fakeTools{output: answers}, map[Version]string{testRelease: testHead})

	if status := run.Status(context.Background(), tagRelease); status.State != Done {
		t.Errorf("the tag was %s: %s", status.State, status.Detail)
	}
}

func TestATagOffTheReleaseBranchIsNotDone(t *testing.T) {
	const offBranch = "cafecafecafecafecafecafecafecafecafecafe"

	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
	answers[branchQuery(offBranch)] = "1\n"

	run := tagRun(t, &fakeTools{output: answers}, map[Version]string{testRelease: offBranch})

	status := run.Status(context.Background(), tagRelease)
	if status.State != Blocked || !strings.Contains(status.Detail, "burn") {
		t.Errorf("a tag off the release branch was %s: %s", status.State, status.Detail)
	}
}

func TestATagOnTheWrongCommitIsNotSomethingTaggingFixes(t *testing.T) {
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = manifest

	run := tagRun(t, &fakeTools{output: answers}, map[Version]string{testRelease: testHead})

	status := run.Status(context.Background(), tagRelease)
	if status.State != Blocked {
		t.Fatalf("a tag carrying the wrong version was %s: %s", status.State, status.Detail)
	}

	// Nothing moves a pushed tag, so the only way on is a new version.
	if !strings.Contains(status.Detail, "burn") || !strings.Contains(status.Detail, "1.25.1") {
		t.Errorf("the detail does not say to burn the version: %s", status.Detail)
	}
}

func TestTagWaitsForThePackageRunOnTheBranchHead(t *testing.T) {
	run := tagRun(t, &fakeTools{output: readyToTag(packageRunJSON("in_progress", ""))}, map[Version]string{})

	status := run.Status(context.Background(), tagRelease)
	if status.State != Waiting {
		t.Fatalf("the tag was %s while the branch was building: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "in progress") {
		t.Errorf("the detail does not say what the run is doing: %s", status.Detail)
	}
}

func TestTagIsBlockedWhileThePackageRunIsRed(t *testing.T) {
	run := tagRun(t, &fakeTools{output: readyToTag(packageRunJSON("completed", "failure"))}, map[Version]string{})

	// A flaky job costs a rerun, because the tag builds the same commit and
	// the release ships what that run produces.
	status := run.Status(context.Background(), tagRelease)
	if status.State != Blocked {
		t.Fatalf("the tag was %s after a failed package run: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "/actions/runs/30474476005") {
		t.Errorf("the detail does not name the run to look at: %s", status.Detail)
	}
}

func TestTagGoesToTheReleaseRepositoryNotTheClonesOrigin(t *testing.T) {
	tools := &fakeTools{output: readyToTag(packageRunJSON("completed", "success")), anyCommand: true}
	run := tagRun(t, tools, map[Version]string{})

	if status := run.Status(context.Background(), tagRelease); status.State != Available {
		t.Fatalf("the tag was %s: %s", status.State, status.Detail)
	}

	if err := RunAction(context.Background(), tagRelease, run, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatal(err)
	}

	url := "https://github.com/" + testRepo + ".git"
	want := []string{
		"git fetch " + url + " " + testBranch,
		"git push " + url + " " + testHead + ":refs/tags/v1.25.0",
	}

	for _, call := range want {
		if !containsCall(tools.calls, call) {
			t.Errorf("the tag action never ran %q; it ran %v", call, tools.calls)
		}
	}
}

// TestTheTagIsPushedWhereTheClonePushes covers a clone that fetches the
// release repository over https and pushes to it over ssh.
func TestTheTagIsPushedWhereTheClonePushes(t *testing.T) {
	const repo = "rancher-sandbox/rancher-desktop"

	tools := &fakeTools{output: map[string]string{"git remote --verbose": remoteOutput}}
	run := newRun(&Release{Version: testRelease, Kind: Minor},
		&Profile{Name: "test", GitHub: GitHubResources{Repo: repo}}, repositoryIn(t, "", repo, tools))
	run.Refs = &Refs{Branches: map[Line]string{testRelease.Line(): testHead}}

	operations, err := planTag(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"git fetch https://github.com/" + repo + " " + testBranch,
		"git push git@github.com:" + repo + ".git " + testHead + ":refs/tags/v1.25.0",
	}

	shown := make([]string, 0, len(operations))
	for _, operation := range operations {
		shown = append(shown, operation.Description)
	}

	if !slices.Equal(shown, want) {
		t.Errorf("the tag runs %q, want %q", shown, want)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}

	return false
}

// tagged is a release whose tag is pushed and carries the version, so the
// package-build step is the next one open.
func tagged(build string) map[string]string {
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
	answers[runsQuery("v1.25.0")] = build

	return answers
}

func TestPackageBuildIsDoneWhenTheTagsRunSucceeded(t *testing.T) {
	tools := &fakeTools{output: tagged(packageRunJSON("completed", "success"))}
	run := tagRun(t, tools, map[Version]string{testRelease: testHead})

	if status := run.Status(context.Background(), packageBuild); status.State != Done {
		t.Errorf("the package build was %s: %s", status.State, status.Detail)
	}
}

func TestPackageBuildWaitsWhileTheRunIsGoing(t *testing.T) {
	tools := &fakeTools{output: tagged(packageRunJSON("in_progress", ""))}
	run := tagRun(t, tools, map[Version]string{testRelease: testHead})

	if status := run.Status(context.Background(), packageBuild); status.State != Waiting {
		t.Errorf("the package build was %s while it was running: %s", status.State, status.Detail)
	}
}

func TestPackageBuildOffersARerunOfTheJobsThatFailed(t *testing.T) {
	tools := &fakeTools{output: tagged(packageRunJSON("completed", "failure")), anyCommand: true}
	run := tagRun(t, tools, map[Version]string{testRelease: testHead})

	// The tag push is what starts the run, and nothing moves the tag, so a
	// rerun is the only way on from a failure.
	if status := run.Status(context.Background(), packageBuild); status.State != Available {
		t.Fatalf("the package build was %s after a failure: %s", status.State, status.Detail)
	}

	if err := RunAction(context.Background(), packageBuild, run, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatal(err)
	}

	want := "gh run rerun 30474476005 --repo " + testRepo + " --failed"
	if !containsCall(tools.calls, want) {
		t.Errorf("the rerun action never ran %q; it ran %v", want, tools.calls)
	}
}

func TestPackageBuildWaitsForTheTag(t *testing.T) {
	tools := &fakeTools{output: readyToTag(packageRunJSON("completed", "success"))}
	run := tagRun(t, tools, map[Version]string{})

	status := run.Status(context.Background(), packageBuild)
	if status.State != Blocked {
		t.Fatalf("the package build was %s with no tag: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "step 9") {
		t.Errorf("the detail does not say it waits for the tag: %s", status.Detail)
	}
}
