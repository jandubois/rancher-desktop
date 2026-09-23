// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"testing"
)

// upstreamRefs is `git ls-remote` output captured from the release
// repository, so the tests meet the branch and tag names it really has.
func upstreamRefs(t *testing.T) *Refs {
	t.Helper()

	output, err := os.ReadFile("testdata/ls-remote.txt")
	if err != nil {
		t.Fatal(err)
	}

	return ParseRefs(string(output))
}

func TestRefsKeepReleaseBranchesAndTagsOnly(t *testing.T) {
	refs := upstreamRefs(t)

	if _, ok := refs.Branches[Line{Major: 1, Minor: 24}]; !ok {
		t.Error("release-1.24 is missing")
	}

	for _, version := range []Version{{Major: 1, Minor: 9}, {Major: 1, Minor: 24}} {
		if _, ok := refs.Tags[version]; !ok {
			t.Errorf("v%s is missing", version)
		}
	}

	// Three of the fixture's 30 release-* branches name no release:
	// release-1.17-hackweek, release-1.9-tech-preview and
	// release-1.9-test-release.
	if len(refs.Branches) != 27 {
		t.Errorf("kept %d of the fixture's 27 release lines", len(refs.Branches))
	}
}

func TestHighestLineAndTagCompareByNumber(t *testing.T) {
	refs := upstreamRefs(t)

	line, ok := refs.HighestLine()
	if !ok || line != (Line{Major: 1, Minor: 24}) {
		t.Fatalf("the highest line was %v, %v", line, ok)
	}

	tag, ok := refs.HighestTag(line)
	if !ok || tag != (Version{Major: 1, Minor: 24}) {
		t.Errorf("the highest tag on %s was %v, %v", line, tag, ok)
	}

	// A line's own tags only: 1.9's newest is v1.9.1, not the higher tags
	// that came later on other lines.
	tag, ok = refs.HighestTag(Line{Major: 1, Minor: 9})
	if !ok || tag != (Version{Major: 1, Minor: 9, Patch: 1}) {
		t.Errorf("the highest tag on 1.9 was %v, %v", tag, ok)
	}

	if _, ok := refs.HighestTag(Line{Major: 9, Minor: 9}); ok {
		t.Error("a line with no tag reported one")
	}
}

func TestAnnotatedTagsUseTheirPeeledCommit(t *testing.T) {
	refs := ParseRefs("" +
		"1111111111111111111111111111111111111111\trefs/heads/release-1.25\n" +
		"2222222222222222222222222222222222222222\trefs/tags/v1.25.0\n" +
		"3333333333333333333333333333333333333333\trefs/tags/v1.25.0^{}\n")

	if got := refs.Tags[Version{Major: 1, Minor: 25}]; got != "3333333333333333333333333333333333333333" {
		t.Errorf("v1.25.0 points at %s, not at the commit it peels to", got)
	}
}

func TestAReleaseOpensItsLineUntilAnEarlierOneIsTagged(t *testing.T) {
	first := Version{Major: 1, Minor: 25}
	second := first.NextPatch()

	// With 1.25.0 burned, the line has no tag.
	refs := &Refs{Tags: map[Version]string{}}
	if kind := refs.KindOf(second); kind != Minor {
		t.Errorf("1.25.1 with nothing tagged before it was a %s release", kind)
	}

	refs.Tags[first] = "beef"

	if kind := refs.KindOf(first); kind != Minor {
		t.Errorf("1.25.0 with its own tag pushed was a %s release", kind)
	}

	if kind := refs.KindOf(second); kind != Patch {
		t.Errorf("1.25.1 after v1.25.0 was a %s release", kind)
	}
}
