// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"regexp"
	"strconv"
)

// Kind tells a minor release from a patch. Several steps are for minors only,
// because a patch reuses its line's branch, OBS packages and docs.
type Kind string

const (
	Minor Kind = "minor"
	Patch Kind = "patch"
)

// Line is the X.Y a release belongs to. It names the release branch, the OBS
// packages and the docs version, so the steps that are per-line rather than
// per-release are keyed by it.
type Line struct {
	Major int
	Minor int
}

// Version is a release version, X.Y.Z.
type Version struct {
	Major int
	Minor int
	Patch int
}

var (
	branchPattern  = regexp.MustCompile(`^release-(\d+)\.(\d+)$`)
	versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)
)

func (l Line) String() string { return fmt.Sprintf("%d.%d", l.Major, l.Minor) }

// Branch is the release branch for the line.
func (l Line) Branch() string { return "release-" + l.String() }

// Release is the line's first version, X.Y.0.
func (l Line) Release() Version { return Version{Major: l.Major, Minor: l.Minor} }

// Next is the line after this one.
func (l Line) Next() Line { return Line{Major: l.Major, Minor: l.Minor + 1} }

// CompareLines orders two lines by number, so release-1.9 sorts before
// release-1.24 rather than after it as strings do.
func CompareLines(a, b Line) int {
	if a.Major != b.Major {
		return a.Major - b.Major
	}

	return a.Minor - b.Minor
}

// ParseBranch reads a release branch name, reporting whether it is one.
func ParseBranch(name string) (Line, bool) {
	match := branchPattern.FindStringSubmatch(name)
	if match == nil {
		return Line{}, false
	}

	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])

	return Line{Major: major, Minor: minor}, true
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Tag is the git tag for the version. Release tags carry a leading v; nothing
// else the tool writes does.
func (v Version) Tag() string { return "v" + v.String() }

// Line is the line the version belongs to.
func (v Version) Line() Line { return Line{Major: v.Major, Minor: v.Minor} }

// NextPatch is the next version on the same line.
func (v Version) NextPatch() Version {
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}

// CompareVersions orders two versions by number.
func CompareVersions(a, b Version) int {
	if c := CompareLines(a.Line(), b.Line()); c != 0 {
		return c
	}

	return a.Patch - b.Patch
}

// ParseVersion reads a release version, with or without the tag's leading v.
func ParseVersion(s string) (Version, error) {
	match := versionPattern.FindStringSubmatch(s)
	if match == nil {
		return Version{}, fmt.Errorf("%q is not a release version", s)
	}

	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}
