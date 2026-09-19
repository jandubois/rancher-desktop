// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// checklist is the release process, in the order a release runs it.
var checklist = []*Step{releaseBranch, versionBump, draftRelease}

// findStep is the step with a checklist number.
func findStep(id string) (*Step, bool) {
	for _, step := range checklist {
		if step.ID == id {
			return step, true
		}
	}

	return nil, false
}

// releaseBranch is step 1. Every release of a line is cut from this branch,
// and creating it starts the package workflow, whose Linux zip the OBS dev
// package builds from.
var releaseBranch = &Step{
	ID:    "1",
	Title: "Release branch",
	Kinds: []Kind{Minor},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies:      "Minor releases. A patch is cut from the branch its line already has.",
		Check:        "{repo} has the branch {branch}.",
		Precondition: "gh can push to {repo}.",
		Instructions: "Push the head of main to the new branch:\n\n" +
			"    git push <url of {repo}> <head of main>:refs/heads/{branch}\n\n" +
			"Check the head commit's subject, date and checks first. It is what " +
			"the release ships. The push starts the package workflow, which " +
			"uploads the branch's Linux zip to the OBS bucket.",
	},
	Check: func(_ context.Context, run *Run) (Answer, error) {
		line := run.Release.Version.Line()
		if commit, ok := run.Refs.Branches[line]; ok {
			return Answer{OK: true, Detail: fmt.Sprintf("%s is at %.7s", line.Branch(), commit)}, nil
		}

		return Answer{Detail: "no " + line.Branch() + " branch"}, nil
	},
}

// versionBump is step 4. The release branch carries the version it ships, and
// when a release has no other commit of its own, the bump is the one commit
// the tag can point at that is not already in main.
var versionBump = &Step{
	ID:    "4",
	Title: "Version bump",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: "Every release.",
		Check:   "package.json on {branch} says {version}.",
		Precondition: "gh can push to {repo}, and the release branch step is " +
			"done or does not apply.",
		Instructions: "Set the `version` field of package.json on {branch} to " +
			"{version}, commit it with a sign-off, push the commit to a branch " +
			"of your own, and open a pull request against {branch} titled " +
			"\"Bump version to {version}\". A reviewer approves and merges it.",
	},
	Check: func(ctx context.Context, run *Run) (Answer, error) {
		branch := run.Release.Branch()

		content, err := run.Repo.FileOnBranch(ctx, branch, "package.json")
		if errors.Is(err, errNotFound) {
			return Answer{Detail: branch + " has no package.json"}, nil
		}

		if err != nil {
			return Answer{}, err
		}

		version, err := readPackageVersion(content)
		if err != nil {
			return Answer{}, err
		}

		if version == run.Release.Version {
			return Answer{OK: true, Detail: fmt.Sprintf("package.json on %s says %s", branch, version)}, nil
		}

		return Answer{Detail: bumpPending(ctx, run, version)}, nil
	},
	Precondition: func(ctx context.Context, run *Run) (Answer, error) {
		return waitFor(ctx, run, releaseBranch), nil
	},
	Action: &Action{
		Title: "Open a pull request bumping package.json to {version}",
		Plan:  planVersionBump,
	},
}

// bumpPending says what the branch says today, and names the pull request
// that would change it, so the wait for a reviewer is visible.
func bumpPending(ctx context.Context, run *Run, found Version) string {
	still := fmt.Sprintf("package.json on %s says %s", run.Release.Branch(), found)

	owner, _, err := run.Repo.PushTarget(ctx)
	if err != nil {
		return still
	}

	number, err := run.Repo.OpenPR(ctx, owner, bumpBranch(run.Release.Version))
	if err != nil || number == 0 {
		return still
	}

	return fmt.Sprintf("PR #%d is open; %s", number, still)
}

// bumpBranch is the branch the bump commit is pushed to.
func bumpBranch(version Version) string { return "bump-to-" + version.String() }

func planVersionBump(ctx context.Context, run *Run) ([]Operation, error) {
	branch := run.Release.Branch()
	head := run.Refs.Branches[run.Release.Version.Line()]

	owner, forkURL, err := run.Repo.PushTarget(ctx)
	if err != nil {
		return nil, err
	}

	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(cache, "bump")
	title := "Bump version to " + run.Release.Version.String()

	return []Operation{
		command("", "git", "fetch", run.Repo.url, branch),
		{
			Description: fmt.Sprintf("check %.7s out in %s", head, dir),
			Do: func(ctx context.Context, run *Run) error {
				_, err := worktreeAt(ctx, run, "bump", head)

				return err
			},
		},
		{
			Description: "set the version in package.json to " + run.Release.Version.String(),
			Do: func(context.Context, *Run) error {
				return writePackageVersion(dir, run.Release.Version)
			},
		},
		command(dir, "git", "commit", "--signoff", "--message", title, "--", "package.json"),
		command(dir, "git", "push", forkURL, "HEAD:refs/heads/"+bumpBranch(run.Release.Version)),
		command("", "gh", "pr", "create", "--repo", run.Repo.repo,
			"--base", branch, "--head", owner+":"+bumpBranch(run.Release.Version),
			"--title", title, "--body", "The release branch carries the version it ships."),
	}, nil
}

// writePackageVersion sets the version in a worktree's package.json.
func writePackageVersion(dir string, version Version) error {
	path := filepath.Join(dir, "package.json")

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, err := setPackageVersion(content, version)
	if err != nil {
		return err
	}

	return os.WriteFile(path, updated, 0o644)
}

// draftRelease is step 5. The draft holds the release notes and every asset
// until the release is published, and it exists before the tag so that the
// assets have somewhere to go.
var draftRelease = &Step{
	ID:    "5",
	Title: "Draft release",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies:      "Every release.",
		Check:        "A release named {tag} exists in {repo}, as a draft or published.",
		Precondition: "gh can push to {repo}.",
		Instructions: "Create the draft, with no --target:\n\n" +
			"    gh release create {tag} --repo {repo} --draft --title \"<title>\" --notes-file <file>\n\n" +
			"The title is \"Rancher Desktop X.Y\" for a minor release and " +
			"\"Rancher Desktop X.Y.Z\" for a patch. Drafts are visible only to " +
			"users who can push, so nobody sees the notes before the release.",
	},
	Check: func(ctx context.Context, run *Run) (Answer, error) {
		state, err := run.Repo.ReleaseState(ctx, run.Release.Tag())
		if err != nil {
			return Answer{}, err
		}

		if state == ReleaseMissing {
			return Answer{Detail: "no release named " + run.Release.Tag()}, nil
		}

		return Answer{OK: true, Detail: fmt.Sprintf("%s is %s", run.Release.Tag(), state)}, nil
	},
}
