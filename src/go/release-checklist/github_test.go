// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeTools answers each command line from a table of output captured from
// real runs.
type fakeTools struct {
	output map[string]string
	stderr map[string]string
	absent map[string]bool
	// anyCommand lets a command nobody gave an answer for succeed, for
	// tests about what runs rather than about what it prints.
	anyCommand bool
	// calls records every command line, in order.
	calls []string
	// ranIn is the directories each command line ran in, in order, for the
	// steps that work in a clone other than the one the tool runs in. A line
	// that ran more than once keeps every one, so a caller passing the right
	// directory once covers for one passing the wrong one.
	ranIn map[string][]string
}

func (f *fakeTools) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)

	if stderr, ok := f.stderr[line]; ok {
		return nil, &commandFailure{Command: line, Stderr: stderr, Err: errExit}
	}

	output, ok := f.output[line]
	if !ok && !f.anyCommand {
		return nil, &commandFailure{Command: line, Stderr: "no answer for this command", Err: errExit}
	}

	return []byte(output), nil
}

func (f *fakeTools) runIn(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	if f.ranIn == nil {
		f.ranIn = map[string][]string{}
	}

	line := strings.Join(append([]string{name}, args...), " ")
	f.ranIn[line] = append(f.ranIn[line], dir)

	return f.run(ctx, name, args...)
}

// ranOnlyIn fails unless a command line ran, and ran in one directory every
// time.
func ranOnlyIn(t *testing.T, tools *fakeTools, line, dir string) {
	t.Helper()

	where := tools.ranIn[line]
	if len(where) == 0 {
		t.Errorf("%s never ran", line)
	}

	for _, ran := range where {
		if ran != dir {
			t.Errorf("%s ran in %q, not %q", line, ran, dir)
		}
	}
}

// runTo answers as the other two do, and writes the answer where a real
// command's output would go.
func (f *fakeTools) runTo(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	output, err := f.runIn(ctx, dir, name, args...)
	if _, written := out.Write(output); written != nil {
		return written
	}

	return err
}

func (f *fakeTools) installed(name string) bool { return !f.absent[name] }

// errExit stands in for the exit status of a command that failed.
var errExit = &exitStatus{}

type exitStatus struct{}

func (*exitStatus) Error() string { return "exit status 1" }

// remoteOutput is `git remote --verbose` from a clone that has the release
// repository under a name of its own, fetched over https without the .git
// suffix and pushed to over ssh, and one remote that is a local path.
const remoteOutput = "origin\thttps://github.com/me/rancher-desktop.git (fetch)\n" +
	"origin\thttps://github.com/me/rancher-desktop.git (push)\n" +
	"rd2\t../rancher-desktop-2 (fetch)\n" +
	"rd2\t../rancher-desktop-2 (push)\n" +
	"upstream\thttps://github.com/rancher-sandbox/rancher-desktop (fetch)\n" +
	"upstream\tgit@github.com:rancher-sandbox/rancher-desktop.git (push)\n"

func TestRemoteIsFoundByURLNotByName(t *testing.T) {
	tools := &fakeTools{output: map[string]string{"git remote --verbose": remoteOutput}}

	repo := repositoryIn(t, "", "rancher-sandbox/rancher-desktop", tools)
	if repo.url != "https://github.com/rancher-sandbox/rancher-desktop" {
		t.Errorf("fetches from %q, not the remote pointing at the release repository", repo.url)
	}

	if repo.pushURL != "git@github.com:rancher-sandbox/rancher-desktop.git" {
		t.Errorf("pushes to %q, not the push URL of the remote pointing at the release repository", repo.pushURL)
	}

	// The fork's own remote is the one the user has already pushed to.
	repo = repositoryIn(t, "", "me/rancher-desktop", tools)
	if repo.url != "https://github.com/me/rancher-desktop.git" {
		t.Errorf("found %q, not the fork's own remote", repo.url)
	}

	// A clone with no remote for the repository still reads its public refs.
	repo = repositoryIn(t, "", "someone/rancher-desktop", tools)
	if repo.url != "https://github.com/someone/rancher-desktop.git" {
		t.Errorf("fell back to %q", repo.url)
	}
}

// TestAPartialCloneRemoteIsFound covers a remote cloned with a filter, whose
// fetch line names the filter after the URL.
func TestAPartialCloneRemoteIsFound(t *testing.T) {
	tools := &fakeTools{output: map[string]string{
		"git remote --verbose": "upstream\thttps://github.com/rancher-sandbox/rancher-desktop (fetch) [blob:none]\n" +
			"upstream\tgit@github.com:rancher-sandbox/rancher-desktop.git (push)\n",
	}}

	repo := repositoryIn(t, "", "rancher-sandbox/rancher-desktop", tools)
	if repo.pushURL != "git@github.com:rancher-sandbox/rancher-desktop.git" {
		t.Errorf("pushes to %q, not the partial clone's own push URL", repo.pushURL)
	}
}

// TestRemotesThatCannotBeReadAreAnError covers a clone path that is no clone,
// a mistake in the settings that falling back to the public URL would hide.
func TestRemotesThatCannotBeReadAreAnError(t *testing.T) {
	tools := &fakeTools{stderr: map[string]string{
		"git remote --verbose": "fatal: not a git repository (or any of the parent directories): .git",
	}}

	if repo, err := newRepository(t.Context(), "/clones/gone", "rancher-sandbox/rancher-desktop", tools); err == nil {
		t.Errorf("remotes nobody could read gave %q", repo.url)
	}
}

// repositoryIn is the repository as a clone's remotes reach it.
func repositoryIn(t *testing.T, clone, repo string, tools commander) *repository {
	t.Helper()

	repository, err := newRepository(t.Context(), clone, repo, tools)
	if err != nil {
		t.Fatal(err)
	}

	return repository
}

func TestRemotesAreReadInTheRepositorysClone(t *testing.T) {
	const clone = "/clones/docs"

	tools := &fakeTools{output: map[string]string{"git remote --verbose": remoteOutput}}

	repositoryIn(t, clone, "rancher-sandbox/rancher-desktop", tools)

	ranOnlyIn(t, tools, "git remote --verbose", clone)
}

func TestForkHasItsOwnRemote(t *testing.T) {
	const push = "git@github.com:me/rancher-desktop.git"

	tools := &fakeTools{output: map[string]string{
		// The fork's remote pushes over ssh.
		"git remote --verbose": strings.Replace(remoteOutput,
			"origin\thttps://github.com/me/rancher-desktop.git (push)", "origin\t"+push+" (push)", 1),
		"gh api user --jq .login":                                "me\n",
		"gh api repos/me/rancher-desktop --jq .parent.full_name": "rancher-sandbox/rancher-desktop\n",
	}}

	repo := repositoryIn(t, "", "rancher-sandbox/rancher-desktop", tools)

	fork, err := repo.Fork(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if fork.url != "https://github.com/me/rancher-desktop.git" || fork.pushURL != push {
		t.Errorf("the fork fetches from %q and pushes to %q, not its own remote's URLs", fork.url, fork.pushURL)
	}

	if owner, url, err := repo.PushTarget(context.Background()); err != nil || owner != "me" || url != push {
		t.Errorf("a step's own branch goes to %q at %q, %v", owner, url, err)
	}
}

func TestOnlyAMissingForkMeansThereIsNoFork(t *testing.T) {
	const parentQuery = "gh api repos/me/rancher-desktop --jq .parent.full_name"

	answers := func(stderr string) *fakeTools {
		return &fakeTools{
			output: map[string]string{
				"git remote --verbose":    remoteOutput,
				"gh api user --jq .login": "me\n",
			},
			stderr: map[string]string{parentQuery: stderr},
		}
	}

	repo := repositoryIn(t, "", "rancher-sandbox/rancher-desktop", answers("gh: Not Found (HTTP 404)"))

	owner, _, err := repo.PushTarget(context.Background())
	if err != nil || owner != "rancher-sandbox" {
		t.Errorf("with no fork the push target is %q, %v", owner, err)
	}

	// A lookup that failed for any other reason says nothing about the fork,
	// and guessing would send a branch to the repository itself.
	repo = repositoryIn(t, "", "rancher-sandbox/rancher-desktop", answers("gh: API rate limit exceeded (HTTP 403)"))

	if owner, _, err = repo.PushTarget(context.Background()); err == nil {
		t.Errorf("a failed fork lookup made %q the push target", owner)
	}
}

func TestReleaseStateReadsDraftPublishedAndMissing(t *testing.T) {
	const view = "gh release view %s --repo rancher-sandbox/rancher-desktop --json " + releaseFields

	run := &fakeTools{
		output: map[string]string{
			strings.Replace(view, "%s", "v1.24.0", 1): "{\"isDraft\":false}\n",
			strings.Replace(view, "%s", "v1.25.0", 1): "{\"isDraft\":true}\n",
		},
		stderr: map[string]string{
			strings.Replace(view, "%s", "v9.9.9", 1): "release not found",
		},
	}
	repo := &repository{repo: "rancher-sandbox/rancher-desktop", run: run}

	for tag, want := range map[string]ReleaseState{
		"v1.24.0": ReleasePublished,
		"v1.25.0": ReleaseDraft,
		"v9.9.9":  ReleaseMissing,
	} {
		state, err := repo.ReleaseState(context.Background(), tag)
		if err != nil {
			t.Errorf("%s: %v", tag, err)

			continue
		}

		if state != want {
			t.Errorf("%s is %s, not %s", tag, state, want)
		}
	}
}

func TestTagInMainReadsTheCommitsMainIsMissing(t *testing.T) {
	const compare = "gh api repos/rancher-sandbox/rancher-desktop/compare/%s...main --jq .behind_by"

	run := &fakeTools{output: map[string]string{
		strings.Replace(compare, "%s", "v1.24.0", 1): "0\n",
		strings.Replace(compare, "%s", "v1.25.0", 1): "12\n",
	}}
	repo := &repository{repo: "rancher-sandbox/rancher-desktop", run: run}

	for tag, want := range map[string]bool{"v1.24.0": true, "v1.25.0": false} {
		merged, err := repo.TagInMain(context.Background(), tag)
		if err != nil {
			t.Errorf("%s: %v", tag, err)

			continue
		}

		if merged != want {
			t.Errorf("%s in main: %v, want %v", tag, merged, want)
		}
	}
}

func TestGitHubsNotThereAnswersAreReadAsAnswers(t *testing.T) {
	const file = "gh api repos/rancher-sandbox/rancher-desktop/contents/package.json?ref=release-1.25 " +
		"--header Accept: application/vnd.github.raw"

	// gh ends an API error with the status. The prose before it varies: a
	// missing branch reads "No commit found for the ref", which no match on
	// the words "not found" catches.
	run := &fakeTools{stderr: map[string]string{
		file: "gh: No commit found for the ref release-1.25 (HTTP 404)",
	}}
	repo := &repository{repo: "rancher-sandbox/rancher-desktop", run: run}

	_, err := repo.FileAtRef(context.Background(), "release-1.25", "package.json")
	if !errors.Is(err, errNotFound) {
		t.Errorf("a missing branch gave %v", err)
	}
}
