// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
)

// burnedPrefix marks the draft left behind when a bad tag is deleted. A
// pushed tag is never moved: the version is burned and the release ships as
// the next patch. The draft carries the marker because it creates no tag, so
// the package workflow never builds it.
const burnedPrefix = "burned-"

// maxBurnedInARow bounds the search for a version nobody has burned, so a
// surprising answer from GitHub cannot loop forever.
const maxBurnedInARow = 100

// Release is the release the tool drives.
type Release struct {
	Version Version
	Kind    Kind
	// Published is true once the release is out of draft and only its
	// post-publish steps are left.
	Published bool
	// Finished is true once the tag is in main, which detection never picks
	// and VERSION can name. Its checks still read; its actions refuse.
	Finished bool
	// Warnings are facts about the repository that the dashboard shows
	// beside the release, because they change what the next steps mean.
	Warnings []string
}

// Branch is the release branch the release is cut from.
func (r *Release) Branch() string { return r.Version.Line().Branch() }

// Tag is the release's git tag.
func (r *Release) Tag() string { return r.Version.Tag() }

// State is the release's kind and how far along it is, for the line that
// names the release being driven.
func (r *Release) State() string {
	state := string(r.Kind)

	switch {
	case r.Finished:
		state += ", finished"
	case r.Published:
		state += ", published"
	}

	return state
}

// Detect finds the release in progress. It starts from the highest release
// branch, because that is where a release is worked on, and asks what has
// happened to it:
//
//   - The branch has no tag of its own: its first release, X.Y.0, is the
//     target.
//   - The newest tag on the branch has a draft or no release: that release is
//     still in progress, and commits past the tag only warn.
//   - The tag is published but has not reached the default branch: the
//     release is still the target, for its post-publish steps.
//   - The branch has commits past a finished release: the next patch.
//   - Otherwise: a new minor from the default branch.
//
// A burned version is never a target; the search moves on to the next patch.
func Detect(ctx context.Context, facts repoFacts) (*Release, error) {
	refs, err := facts.Refs(ctx)
	if err != nil {
		return nil, err
	}

	line, found := refs.HighestLine()
	if !found {
		return nil, errors.New("the repository has no release branch")
	}

	version, published, warnings, err := target(ctx, facts, refs, line)
	if err != nil {
		return nil, err
	}

	if !published {
		if version, err = skipBurned(ctx, facts, version); err != nil {
			return nil, err
		}
	}

	return &Release{
		Version:   version,
		Kind:      refs.KindOf(version),
		Published: published,
		Warnings:  warnings,
	}, nil
}

func target(ctx context.Context, facts repoFacts, refs *Refs, line Line) (Version, bool, []string, error) {
	tag, tagged := refs.HighestTag(line)
	if !tagged {
		return line.Release(), false, nil, nil
	}

	state, err := facts.ReleaseState(ctx, tag.Tag())
	if err != nil {
		return Version{}, false, nil, err
	}

	aheadOfTag := refs.Branches[line] != refs.Tags[tag]

	if state != ReleasePublished {
		var warnings []string
		if aheadOfTag {
			warnings = append(warnings, fmt.Sprintf(
				"%s has commits past %s; they go into %s unless the tag is burned",
				line.Branch(), tag.Tag(), tag.NextPatch()))
		}

		return tag, false, warnings, nil
	}

	inMain, err := facts.TagInMain(ctx, tag.Tag())
	if err != nil {
		return Version{}, false, nil, err
	}

	switch {
	case !inMain:
		return tag, true, nil, nil
	case aheadOfTag:
		return tag.NextPatch(), false, nil, nil
	default:
		return line.Next().Release(), false, nil, nil
	}
}

// skipBurned passes over the versions somebody has burned, so a deleted tag
// cannot be released again under its old number.
func skipBurned(ctx context.Context, facts repoFacts, version Version) (Version, error) {
	for range maxBurnedInARow {
		state, err := facts.ReleaseState(ctx, burnedPrefix+version.Tag())
		if err != nil {
			return Version{}, err
		}

		if state == ReleaseMissing {
			return version, nil
		}

		version = version.NextPatch()
	}

	return Version{}, fmt.Errorf("every version up to %s is burned", version)
}
