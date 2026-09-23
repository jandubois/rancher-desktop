// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import "strings"

// Refs is what the release repository's refs say: the release branches it
// has and the release tags, each with the commit it points at. One
// `git ls-remote` answers both, so the checks that only ask "does this
// branch exist" or "which commit does this tag name" need no API call.
type Refs struct {
	// Branches maps each release line to its branch head.
	Branches map[Line]string
	// Tags maps each release version to the commit it tags.
	Tags map[Version]string
}

// ParseRefs reads `git ls-remote` output, keeping the release branches and
// the release tags. An annotated tag's peeled ref wins over the tag object,
// so the commit is always a commit; release tags are lightweight today, but
// nothing enforces that.
func ParseRefs(output string) *Refs {
	refs := &Refs{
		Branches: map[Line]string{},
		Tags:     map[Version]string{},
	}

	peeled := map[Version]bool{}

	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		commit, name, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found {
			continue
		}

		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			if branch, ok := ParseBranch(strings.TrimPrefix(name, "refs/heads/")); ok {
				refs.Branches[branch] = commit
			}
		case strings.HasPrefix(name, "refs/tags/"):
			tag := strings.TrimPrefix(name, "refs/tags/")
			isPeeled := strings.HasSuffix(tag, "^{}")
			tag = strings.TrimSuffix(tag, "^{}")

			version, err := ParseVersion(tag)
			if err != nil || !strings.HasPrefix(tag, "v") {
				continue
			}

			if isPeeled || !peeled[version] {
				refs.Tags[version] = commit
			}

			peeled[version] = peeled[version] || isPeeled
		}
	}

	return refs
}

// HighestLine is the newest release line the repository has a branch for.
func (r *Refs) HighestLine() (Line, bool) {
	var highest Line

	found := false

	for line := range r.Branches {
		if !found || CompareLines(line, highest) > 0 {
			highest, found = line, true
		}
	}

	return highest, found
}

// HighestTag is the newest release tag on the line. It counts only the
// line's own tags: the nearest tag in a new branch's history belongs to the
// previous line, and taking that one would drive the wrong release.
func (r *Refs) HighestTag(line Line) (Version, bool) {
	var highest Version

	found := false

	for version := range r.Tags {
		if version.Line() != line {
			continue
		}

		if !found || CompareVersions(version, highest) > 0 {
			highest, found = version, true
		}
	}

	return highest, found
}

// KindOf reports whether a release opens its line or patches one. A release
// opens its line when no earlier version on the line has a tag, which is also
// true of X.Y.1 once X.Y.0 is burned.
func (r *Refs) KindOf(version Version) Kind {
	if first, found := r.LowestTag(version.Line()); found && CompareVersions(first, version) < 0 {
		return Patch
	}

	return Minor
}

// LowestTag is the oldest release tag on the line, which is the release the
// line opened with.
func (r *Refs) LowestTag(line Line) (Version, bool) {
	var lowest Version

	found := false

	for version := range r.Tags {
		if version.Line() != line {
			continue
		}

		if !found || CompareVersions(version, lowest) < 0 {
			lowest, found = version, true
		}
	}

	return lowest, found
}
