// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"strings"
)

// githubRepo is the repository the release is cut from. Its probe asks only
// to read, so done work shows as done to anyone who can see it.
var githubRepo = githubResource("GitHub repo", hasRepo,
	func(ctx context.Context, run *Run) (Answer, error) {
		return access(ctx, run, run.Profile.GitHub.Repo, "pull")
	})

// githubRepoPush is the release repository for a step that changes it, or
// whose check reads the draft release, which only someone who can push sees.
var githubRepoPush = githubResource("GitHub repo", hasRepo,
	func(ctx context.Context, run *Run) (Answer, error) {
		return access(ctx, run, run.Profile.GitHub.Repo, "push")
	})

// githubFork is where a step pushes its own branch. That is the user's fork
// of the release repository, or the repository itself when they have none.
var githubFork = githubResource("GitHub fork", nil,
	func(ctx context.Context, run *Run) (Answer, error) {
		fork, err := run.Repo.Fork(ctx)
		if err != nil {
			return Answer{}, err
		}

		return access(ctx, run, fork.repo, "push")
	})

// githubDocsRepo is the repository the documentation site is built from. A
// minor release adds a page of its own to it.
var githubDocsRepo = githubResource("GitHub docs repo",
	func(p *Profile) bool { return p.GitHub.DocsRepo != "" },
	func(ctx context.Context, run *Run) (Answer, error) {
		return access(ctx, run, run.Profile.GitHub.DocsRepo, "pull")
	})

// githubDocsFork is where the documentation steps push. That is the user's
// fork of the documentation repository, or the repository itself when they
// have none.
var githubDocsFork = githubResource("GitHub docs fork", nil,
	func(ctx context.Context, run *Run) (Answer, error) {
		fork, err := docsFork(ctx, run)
		if err != nil {
			return Answer{}, err
		}

		return access(ctx, run, fork.repo, "push")
	})

// githubResource is a GitHub repository reached through gh, which keeps the
// credentials.
func githubResource(name string, configured func(*Profile) bool,
	probe func(context.Context, *Run) (Answer, error),
) *Resource {
	return &Resource{
		Name:       name,
		Command:    "gh",
		Install:    "https://cli.github.com",
		Configured: configured,
		Probe:      probe,
	}
}

// hasRepo reports whether the profile names a release repository.
func hasRepo(p *Profile) bool { return p.GitHub.Repo != "" }

// access reports whether the user gh is signed in as holds a permission on a
// repository, pull to read it or push to change it.
func access(ctx context.Context, run *Run, repo, permission string) (Answer, error) {
	output, err := run.Tools.run(ctx, "gh", "api", "repos/"+repo, "--jq", ".permissions."+permission)
	if err != nil {
		return Answer{}, fmt.Errorf("reading your access to %s: %w", repo, err)
	}

	if strings.TrimSpace(string(output)) != "true" {
		return Answer{Detail: fmt.Sprintf(
			"no %s access to %s; run gh auth login as a user who has it", permission, repo)}, nil
	}

	return Answer{OK: true, Detail: permission + " access to " + repo}, nil
}
