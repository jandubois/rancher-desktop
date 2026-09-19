// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// defaultBranch is the branch a release is merged back into, and the one a
// new line branches from.
const defaultBranch = "main"

// ReleaseState is what GitHub knows about the release for a tag.
type ReleaseState string

const (
	// ReleaseMissing means no release names the tag.
	ReleaseMissing ReleaseState = "missing"
	// ReleaseDraft means the release exists but only maintainers see it.
	ReleaseDraft ReleaseState = "draft"
	// ReleasePublished means the release is out.
	ReleasePublished ReleaseState = "published"
)

// repoFacts is what the release detection asks about the release repository.
// It is an interface so the tests can answer with output captured from real
// runs.
type repoFacts interface {
	// Refs lists the release branches and release tags with their commits.
	Refs(ctx context.Context) (*Refs, error)
	// ReleaseState reports whether a release names the tag, and whether it
	// is still a draft.
	ReleaseState(ctx context.Context, tag string) (ReleaseState, error)
	// TagInMain reports whether the tag's commit has reached the default
	// branch, which is what marks a release done.
	TagInMain(ctx context.Context, tag string) (bool, error)
}

// repository reaches the profile's repository through git and gh, so both
// keep their own credentials.
type repository struct {
	repo string
	// url is the remote to read refs from. Remotes are found by URL, never
	// by name, so a clone that calls the release repository anything at all
	// still works.
	url string
	run commander
	// refs is read once and shared by every step that checks a branch or a
	// tag, so one refresh makes one call.
	refs *Refs
}

func newRepository(ctx context.Context, repo string, run commander) *repository {
	return &repository{repo: repo, url: remoteURL(ctx, repo, run), run: run}
}

// remoteURL is the URL of the clone's remote for the repository, or the
// repository's public URL when no remote points at it.
func remoteURL(ctx context.Context, repo string, run commander) string {
	output, err := run.run(ctx, "git", "remote", "--verbose")
	if err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			if sameRepo(fields[1], repo) {
				return fields[1]
			}
		}
	}

	return "https://github.com/" + repo + ".git"
}

// sameRepo reports whether a git remote URL points at the owner/name
// repository, in any of the spellings a clone may hold.
func sameRepo(url, repo string) bool {
	trimmed := strings.TrimSuffix(url, ".git")
	if host, path, found := strings.Cut(trimmed, ":"); found && !strings.Contains(host, "/") {
		trimmed = path
	}

	return strings.EqualFold(strings.TrimPrefix(trimmed, "https://github.com/"), repo)
}

func (r *repository) Refs(ctx context.Context) (*Refs, error) {
	if r.refs != nil {
		return r.refs, nil
	}

	output, err := r.run.run(ctx, "git", "ls-remote", r.url, "refs/heads/release-*", "refs/tags/v*")
	if err != nil {
		return nil, fmt.Errorf("listing the refs of %s: %w", r.repo, err)
	}

	r.refs = ParseRefs(string(output))

	return r.refs, nil
}

func (r *repository) ReleaseState(ctx context.Context, tag string) (ReleaseState, error) {
	output, err := r.run.run(ctx, "gh", "release", "view", tag, "--repo", r.repo, "--json", "isDraft")
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && failure.says("release not found") {
			return ReleaseMissing, nil
		}

		return "", fmt.Errorf("reading the release %s: %w", tag, err)
	}

	var release struct {
		IsDraft bool `json:"isDraft"`
	}

	if err := json.Unmarshal(output, &release); err != nil {
		return "", fmt.Errorf("reading the release %s: %w", tag, err)
	}

	if release.IsDraft {
		return ReleaseDraft, nil
	}

	return ReleasePublished, nil
}

func (r *repository) TagInMain(ctx context.Context, tag string) (bool, error) {
	path := fmt.Sprintf("repos/%s/compare/%s...%s", r.repo, tag, defaultBranch)

	output, err := r.run.run(ctx, "gh", "api", path, "--jq", ".behind_by")
	if err != nil {
		return false, fmt.Errorf("comparing %s with %s: %w", tag, defaultBranch, err)
	}

	// behind_by counts the commits the tag has that the default branch does
	// not, so zero means the tag has been merged back.
	behind, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return false, fmt.Errorf("comparing %s with %s: %w", tag, defaultBranch, err)
	}

	return behind == 0, nil
}
