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
var checklist = []*Step{
	releaseBranch, versionBump, draftRelease, releaseNotes, tagRelease, packageBuild, linuxAssets,
	windowsAssets,
}

// everyRelease is the applicability of a step that a minor and a patch both
// run.
const everyRelease = "Every release."

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
		Applies: everyRelease,
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

		content, err := run.Repo.FileAtRef(ctx, branch, "package.json")
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
		Applies:      everyRelease,
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

// releaseNotes is step 6. The notes are what a user reads when the release
// arrives, so the tool puts the clone's copy into the release and leaves the
// judgment of whether they are right to a person.
var releaseNotes = &Step{
	ID:    "6",
	Title: "Release notes",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: everyRelease,
		Check: "{tag} has notes, they are the ones in release-notes.md when this " +
			"clone has that file, and you have marked them done. Editing them " +
			"afterwards puts the step back to available.",
		Precondition: "gh can push to {repo}, and the draft release exists.",
		Instructions: "Write the notes in release-notes.md at the top of your clone, " +
			"then put them into the release:\n\n" +
			"    gh release edit {tag} --repo {repo} --notes-file release-notes.md\n\n" +
			"Start from the previous release's notes, which " +
			"`gh release view <previous tag> --repo {repo}` prints, and keep the " +
			"same sections: the installer links for {version}, the new contributors, what " +
			"changed for the user, the bundled utilities that moved, and the compare " +
			"link. Write what a user sees and leave internal work out.\n\n" +
			"Press `f` to write this release's facts out as sections to paste. " +
			"nerdctl and containerd are not among them, because they ship in the " +
			"guest ISO rather than in dependencies.yaml.\n\n" +
			"The file is untracked and nothing ignores it, so keep it out of your " +
			"commits. Press `m` once the notes on the release are the ones to ship.",
	},
	Check: checkReleaseNotes,
	Precondition: func(ctx context.Context, run *Run) (Answer, error) {
		return waitFor(ctx, run, draftRelease), nil
	},
	Confirms: notesInRelease,
	Facts: &Facts{
		Title:  "The bundled utilities that moved, the first-time contributors, and the changelog links",
		File:   factsFile,
		Gather: gatherNotesFacts,
	},
	Action: &Action{
		Title:   "Put release-notes.md into the notes of {tag}",
		Summary: notesChange,
		Plan:    planReleaseNotes,
	},
}

// packageWorkflow builds every platform's release assets. A push to a release
// branch starts one run and the tag push starts another, so the ref is what
// tells two runs of the same commit apart.
const packageWorkflow = "package.yaml"

// tagRelease is step 9. Pushing the tag starts the package run that builds
// every asset the release ships, and the tag is what the merge-back carries
// into main, so a tag on the wrong commit costs the version.
var tagRelease = &Step{
	ID:    "9",
	Title: "Tag",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: everyRelease,
		Check:   "{tag} names a commit of {repo} whose package.json says {version}.",
		Precondition: "gh can push to {repo}, the version bump and the draft release are " +
			"done, {branch} has a commit main does not, and the package run for the " +
			"head of {branch} succeeded.",
		Instructions: "Tag the head of {branch}:\n\n" +
			"    git push <url of {repo}> <head of {branch}>:refs/tags/{tag}\n\n" +
			"Name the release repository by URL. In most clones `origin` is your " +
			"own fork, so a tag pushed there never reaches {repo}. The push starts " +
			"the package workflow for the tag, and that run builds the assets the " +
			"release ships. Nothing moves a tag once it is pushed, so a tag on the " +
			"wrong commit costs the version: it has to be burned, and the release " +
			"goes out as the next patch.",
	},
	Check: func(ctx context.Context, run *Run) (Answer, error) {
		tag := run.Release.Tag()

		commit, tagged := run.Refs.Tags[run.Release.Version]
		if !tagged {
			return Answer{Detail: "no " + tag + " in " + run.Profile.GitHub.Repo}, nil
		}

		content, err := run.Repo.FileAtRef(ctx, tag, "package.json")
		if err != nil {
			return Answer{}, err
		}

		version, err := readPackageVersion(content)
		if err != nil {
			return Answer{}, err
		}

		if version != run.Release.Version {
			return Answer{Detail: fmt.Sprintf(
				"%s is at %.7s, whose package.json says %s", tag, commit, version)}, nil
		}

		return Answer{OK: true, Detail: fmt.Sprintf("%s is at %.7s", tag, commit)}, nil
	},
	Precondition: func(ctx context.Context, run *Run) (Answer, error) {
		// The check has already found the tag wrong, and no step can put
		// that right, because the tool never moves a pushed tag.
		if _, tagged := run.Refs.Tags[run.Release.Version]; tagged {
			return Answer{Detail: fmt.Sprintf(
				"%s is already pushed and nothing moves a tag; burn %s and release %s",
				run.Release.Tag(), run.Release.Version, run.Release.Version.NextPatch())}, nil
		}

		if ready := waitFor(ctx, run, versionBump, draftRelease); !ready.OK {
			return ready, nil
		}

		branch := run.Release.Branch()
		head := run.Refs.Branches[run.Release.Version.Line()]

		// A commit main already has is one the merge-back cannot carry back,
		// and reaching main is what marks a release done, so tagging one
		// would show the release finished the moment it started.
		merged, err := run.Repo.InBranch(ctx, head, defaultBranch)
		if err != nil {
			return Answer{}, err
		}

		if merged {
			return Answer{Detail: fmt.Sprintf(
				"the head of %s is already in %s, so the release has no commit of its own",
				branch, defaultBranch)}, nil
		}

		return packageRun(ctx, run, branch, head)
	},
	Action: &Action{
		Title: "Push {tag} to {repo}",
		Plan:  planTag,
	},
}

// packageRun reports how the package workflow's run for a ref is going: OK
// once it has succeeded, Waiting until it finishes, and otherwise the line
// that says how it ended and where to read it.
func packageRun(ctx context.Context, run *Run, ref, commit string) (Answer, error) {
	build, err := run.Repo.LatestRun(ctx, packageWorkflow, ref, commit)
	if err != nil {
		return Answer{}, err
	}

	switch {
	case build == nil:
		return Answer{Waiting: true, Detail: "no package run for " + ref + " yet"}, nil
	case !build.Finished():
		return Answer{Waiting: true, Detail: fmt.Sprintf(
			"the package run for %s is %s: %s", ref, build.State(), build.URL)}, nil
	case !build.Succeeded():
		return Answer{Detail: fmt.Sprintf(
			"the package run for %s ended in %s: %s", ref, build.State(), build.URL)}, nil
	default:
		return Answer{OK: true, Detail: fmt.Sprintf(
			"the package run for %s succeeded: %s", ref, build.URL)}, nil
	}
}

// planTag pushes the release branch head to the tag. The fetch brings the
// commit into the clone, because git resolves the source of a push locally
// however well the remote knows it.
func planTag(_ context.Context, run *Run) ([]Operation, error) {
	branch := run.Release.Branch()

	// git reads a push with an empty source as a request to delete the
	// destination, so a missing branch head must never reach the push.
	head, ok := run.Refs.Branches[run.Release.Version.Line()]
	if !ok {
		return nil, fmt.Errorf("%s has no %s branch to tag", run.Profile.GitHub.Repo, branch)
	}

	return []Operation{
		command("", "git", "fetch", run.Repo.url, branch),
		command("", "git", "push", run.Repo.url, head+":refs/tags/"+run.Release.Tag()),
	}, nil
}

// packageBuild is step 10. The tag's package run builds and signs every asset
// the release ships, so until it passes the steps that upload them have
// nothing to upload.
var packageBuild = &Step{
	ID:    "10",
	Title: "Package build",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: everyRelease,
		Check:   "The package workflow run for {tag} succeeded.",
		Precondition: "gh can push to {repo}, and {tag} is pushed. Nothing starts " +
			"this run by hand; the tag push does.",
		Instructions: "Wait for the package run the tag push started, at\n\n" +
			"    https://github.com/{repo}/actions/workflows/package.yaml\n\n" +
			"Every release asset comes from that run. Rerun the jobs that failed " +
			"if it does not pass.",
	},
	Check: func(ctx context.Context, run *Run) (Answer, error) {
		commit, tagged := run.Refs.Tags[run.Release.Version]
		if !tagged {
			return Answer{Detail: "no " + run.Release.Tag() + " to build"}, nil
		}

		return packageRun(ctx, run, run.Release.Tag(), commit)
	},
	Precondition: func(ctx context.Context, run *Run) (Answer, error) {
		if ready := waitFor(ctx, run, tagRelease); !ready.OK {
			return ready, nil
		}

		answer, err := packageRun(ctx, run, run.Release.Tag(), run.Refs.Tags[run.Release.Version])
		if err != nil {
			return Answer{}, err
		}

		if answer.Waiting {
			return answer, nil
		}

		// The run is over and the check has already found that it did not
		// pass, so rerunning the jobs that failed is what is left.
		return Answer{OK: true}, nil
	},
	Action: &Action{
		Title: "Rerun the failed jobs of the package run for {tag}",
		Plan:  planRerunPackage,
	},
}

// planRerunPackage reruns only the jobs that failed, so the artifacts the run
// has already built stay where the release steps reach them.
func planRerunPackage(ctx context.Context, run *Run) ([]Operation, error) {
	build, err := run.Repo.LatestRun(ctx, packageWorkflow,
		run.Release.Tag(), run.Refs.Tags[run.Release.Version])
	if err != nil {
		return nil, err
	}

	if build == nil {
		return nil, fmt.Errorf("%s has no package run to rerun", run.Release.Tag())
	}

	return []Operation{
		command("", "gh", "run", "rerun", build.ID, "--repo", run.Repo.repo, "--failed"),
	}, nil
}

// linuxAssets is step 11. Nothing signs the Linux zip, so it goes from the
// build to the release as it was built.
var linuxAssets = &Step{
	ID:    "11",
	Title: "Linux assets",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: everyRelease,
		Check:   "{tag} has rancher-desktop-linux-{tag}.zip and its .sha512sum.",
		Precondition: "gh can push to {repo}, the draft release exists, {tag} is pushed, " +
			"and the package run for {tag} has built its Linux zip.",
		Instructions: "Take the Linux build from the package run for {tag}:\n\n" +
			"    gh run download <run id> --repo {repo} --name \"Rancher Desktop-linux.zip\" --dir <dir>\n\n" +
			"gh unpacks the artifact, so the zip arrives under the name the build " +
			"stamped it with. Rename it to rancher-desktop-linux-{tag}.zip. The " +
			"workflow that copies the release's Linux zip to the OBS bucket builds its " +
			"download URL from that name, so the release cannot keep the build's. Write " +
			"the checksum beside it:\n\n" +
			"    sha512sum rancher-desktop-linux-{tag}.zip > rancher-desktop-linux-{tag}.zip.sha512sum\n\n" +
			"Leave -b off, so the line reads the way every released Linux checksum " +
			"does. macOS has no sha512sum, and shasum -a 512 prints the same line. " +
			"Upload both files:\n\n" +
			"    gh release upload {tag} --repo {repo} <the zip> <the checksum>",
	},
	Check:        checkLinuxAssets,
	Precondition: linuxAssetsReady,
	Action: &Action{
		Title:   "Upload the Linux zip and its checksum to {tag}",
		Summary: linuxDownloadSize,
		Plan:    planLinuxAssets,
	},
}

// windowsAssets is step 14. The Windows signing key is a fob held by one
// person, so the tool gathers what they need and waits for their upload.
var windowsAssets = &Step{
	ID:    "14",
	Title: "Windows assets",
	Kinds: []Kind{Minor, Patch},
	Needs: []*Resource{githubRepo},
	Doc: Doc{
		Applies: everyRelease,
		Check:   "{tag} has Rancher.Desktop.Setup.{version}.msi and its .sha512sum.",
		Precondition: "gh can push to {repo}, the draft release exists, {tag} is pushed, " +
			"and the package run for {tag} has built its Windows zip.",
		Instructions: "The Windows signing key is a fob, so the key holder builds and " +
			"signs the installer on their own machine. Press `f` for the message to " +
			"send them, which names the package run the build is in.\n\n" +
			"They take the \"Rancher Desktop-win.zip\" artifact of that run and sign " +
			"it with the SUSE code-signing certificate, as docs/development/signing.md " +
			"describes. yarn sign writes the installer and its checksum under the " +
			"names the release uses, so nothing has to be renamed or hashed by " +
			"hand.\n\n" +
			"They can upload the two files or send them to you.",
	},
	Check:        checkWindowsAssets,
	Precondition: windowsAssetsReady,
	Facts: &Facts{
		Title:  "What the Windows signer needs",
		File:   windowsSigningFile,
		Gather: gatherWindowsSigning,
	},
}
