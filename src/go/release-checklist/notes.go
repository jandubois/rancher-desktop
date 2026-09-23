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
	"strings"
)

// notesFile is the working copy of the release notes, a file at the top of
// the clone that the notes step puts into the release. It is untracked and
// nothing ignores it.
const notesFile = "release-notes.md"

// draftNotesFile is where the release's own notes are written for the diff
// the action shows, under the release's cache directory.
const draftNotesFile = "notes-in-release.md"

// notesSkeletonFile is where the draft release step writes the notes it
// creates the release with, under the release's cache directory.
const notesSkeletonFile = "notes-skeleton.md"

// notesSkeleton is the notes a release starts with, which hold the opening
// sentence and installer links of the releases before it and the heading the
// release notes go under.
func notesSkeleton(run *Run) string {
	version := run.Release.Version
	download := fmt.Sprintf("https://github.com/%s/releases/download/%s/", run.Repo.repo, run.Release.Tag())

	installers := fmt.Sprintf(`* [Windows](%[1]s%[2]s)
* [macOS x86_64](%[1]s%[3]s)
* [macOS aarch64](%[1]s%[4]s)
`, download, windowsAsset(version), macAsset(version, "x86_64"), macAsset(version, "aarch64"))

	// The Linux instructions are on the documentation site, so a profile that
	// names none gets no link.
	if docs := run.Profile.Docs; docs != nil && docs.Site != "" {
		installers += fmt.Sprintf("* [Linux install notes](%s/%s/getting-started/installation#linux)\n",
			docs.Site, version.Line())
	}

	return fmt.Sprintf(`This is the %[1]s release of Rancher Desktop, an open source desktop application to bring Kubernetes and container management to macOS, Windows, and Linux.

## Installers

%[2]s
## Release Notes for %[1]s
`, version, installers)
}

// notesInClone is the release notes as the clone has them, and whether the
// file is there at all.
func notesInClone(ctx context.Context, run *Run) (string, bool, error) {
	root, err := run.Repo.Root(ctx)
	if err != nil {
		return "", false, err
	}

	content, err := os.ReadFile(filepath.Join(root, notesFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}

	if err != nil {
		return "", false, err
	}

	return string(content), true, nil
}

// sameNotes reports whether two copies of the notes say the same thing.
// GitHub stores a release body with CRLF line endings, and an editor may
// leave the last newline off, so the bytes differ where the text does not.
func sameNotes(a, b string) bool { return notesText(a) == notesText(b) }

// notesText is the notes without the differences no reader sees. A
// confirmation covers this text, so a copy that only came back from GitHub
// with other line endings still counts as the one that was marked done.
func notesText(text string) string {
	return strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

// checkReleaseNotes reports whether the release has the notes this clone has
// written for it.
func checkReleaseNotes(ctx context.Context, run *Run) (Answer, error) {
	tag := run.Release.Tag()

	notes, err := run.Repo.ReleaseNotes(ctx, tag)
	if errors.Is(err, errNotFound) {
		return Answer{Detail: "no release named " + tag}, nil
	}

	if err != nil {
		return Answer{}, err
	}

	if strings.TrimSpace(notes) == "" {
		return Answer{Detail: tag + " has no notes"}, nil
	}

	local, written, err := notesInClone(ctx, run)
	if err != nil {
		return Answer{}, err
	}

	switch {
	case !written:
		return Answer{OK: true, Detail: fmt.Sprintf(
			"%s has notes, and this clone has no %s to compare them with", tag, notesFile)}, nil
	case !sameNotes(local, notes):
		return Answer{Detail: fmt.Sprintf("%s differs from the notes of %s", notesFile, tag)}, nil
	default:
		return Answer{OK: true, Detail: fmt.Sprintf("the notes of %s are the ones in %s", tag, notesFile)}, nil
	}
}

// notesInRelease is the text somebody marks done for the release notes, the
// notes as the release has them, which is what a user will read.
func notesInRelease(ctx context.Context, run *Run) (string, error) {
	notes, err := run.Repo.ReleaseNotes(ctx, run.Release.Tag())
	if errors.Is(err, errNotFound) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return notesText(notes), nil
}

// notesChange is what running the action would change in the release, as git
// prints it. The release's own notes go to a file under the release's cache
// directory, because git compares files.
func notesChange(ctx context.Context, run *Run) (string, error) {
	notes, err := run.Repo.ReleaseNotes(ctx, run.Release.Tag())
	if err != nil {
		return "", err
	}

	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(cache, cachePermissions); err != nil {
		return "", err
	}

	from := filepath.Join(cache, draftNotesFile)
	if err := os.WriteFile(from, []byte(notesText(notes)+"\n"), 0o644); err != nil {
		return "", err
	}

	root, err := run.Repo.Root(ctx)
	if err != nil {
		return "", err
	}

	// git exits non-zero when the files differ, which is the answer here
	// rather than a failure, so the output decides.
	output, err := run.Tools.run(ctx, "git", "diff", "--no-index", "--", from, filepath.Join(root, notesFile))
	if len(output) == 0 && err != nil {
		return "", err
	}

	return string(output), nil
}

// planReleaseNotes puts the clone's notes into the release.
func planReleaseNotes(ctx context.Context, run *Run) ([]Operation, error) {
	tag := run.Release.Tag()

	local, written, err := notesInClone(ctx, run)
	if err != nil {
		return nil, err
	}

	if !written {
		return nil, fmt.Errorf("this clone has no %s to put into %s", notesFile, tag)
	}

	notes, err := run.Repo.ReleaseNotes(ctx, tag)
	if err != nil {
		return nil, err
	}

	if sameNotes(local, notes) {
		return nil, fmt.Errorf("the notes of %s are already the ones in %s", tag, notesFile)
	}

	root, err := run.Repo.Root(ctx)
	if err != nil {
		return nil, err
	}

	return []Operation{
		command("", "gh", "release", "edit", tag, "--repo", run.Repo.repo,
			"--notes-file", filepath.Join(root, notesFile)),
	}, nil
}
