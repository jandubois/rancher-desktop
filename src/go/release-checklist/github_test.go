// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"strings"
	"testing"
)

// fakeTools answers each command line from a table of output captured from
// real runs.
type fakeTools struct {
	output map[string]string
	stderr map[string]string
	absent map[string]bool
}

func (f *fakeTools) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")

	if stderr, ok := f.stderr[line]; ok {
		return nil, &commandFailure{Command: line, Stderr: stderr, Err: errExit}
	}

	output, ok := f.output[line]
	if !ok {
		return nil, &commandFailure{Command: line, Stderr: "no answer for this command", Err: errExit}
	}

	return []byte(output), nil
}

func (f *fakeTools) installed(name string) bool { return !f.absent[name] }

// errExit stands in for the exit status of a command that failed.
var errExit = &exitStatus{}

type exitStatus struct{}

func (*exitStatus) Error() string { return "exit status 1" }

// remoteOutput is `git remote --verbose` from a clone that has the release
// repository under a name of its own, one remote that is a local path, and a
// fetch URL without the .git suffix.
const remoteOutput = "origin\tgit@github.com:me/rancher-desktop.git (fetch)\n" +
	"origin\tgit@github.com:me/rancher-desktop.git (push)\n" +
	"rd2\t../rancher-desktop-2 (fetch)\n" +
	"rd2\t../rancher-desktop-2 (push)\n" +
	"upstream\tgit@github.com:rancher-sandbox/rancher-desktop (fetch)\n" +
	"upstream\tgit@github.com:rancher-sandbox/rancher-desktop.git (push)\n"

func TestRemoteIsFoundByURLNotByName(t *testing.T) {
	run := &fakeTools{output: map[string]string{"git remote --verbose": remoteOutput}}

	url := remoteURL(context.Background(), "rancher-sandbox/rancher-desktop", run)
	if url != "git@github.com:rancher-sandbox/rancher-desktop" {
		t.Errorf("found %q, not the remote pointing at the release repository", url)
	}

	// A clone with no remote for the repository still reads its public refs.
	url = remoteURL(context.Background(), "someone/rancher-desktop", run)
	if url != "https://github.com/someone/rancher-desktop.git" {
		t.Errorf("fell back to %q", url)
	}
}

func TestReleaseStateReadsDraftPublishedAndMissing(t *testing.T) {
	const view = "gh release view %s --repo rancher-sandbox/rancher-desktop --json isDraft"

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
