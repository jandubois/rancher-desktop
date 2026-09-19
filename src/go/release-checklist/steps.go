// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
)

// checklist is the release process, in the order a release runs it.
var checklist = []*Step{releaseBranch, draftRelease}

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
