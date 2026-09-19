// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import "testing"

func TestParseVersionTakesReleaseVersionsOnly(t *testing.T) {
	for _, input := range []string{"v1.24.0", "1.24.0"} {
		version, err := ParseVersion(input)
		if err != nil {
			t.Errorf("%s: %v", input, err)

			continue
		}

		if version != (Version{Major: 1, Minor: 24}) {
			t.Errorf("%s gave %v", input, version)
		}
	}

	// The repository has tags such as v1.9.0-tech-preview and branches such
	// as release-1.17-hackweek, which name no release.
	for _, input := range []string{"v1.9.0-tech-preview", "v1.9.0-test-release-2", "1.24", "v1", ""} {
		if _, err := ParseVersion(input); err == nil {
			t.Errorf("%q was read as a release version", input)
		}
	}
}

func TestParseBranchTakesReleaseBranchesOnly(t *testing.T) {
	line, ok := ParseBranch("release-1.24")
	if !ok || line != (Line{Major: 1, Minor: 24}) {
		t.Errorf("release-1.24 gave %v, %v", line, ok)
	}

	for _, input := range []string{"release-1.9-tech-preview", "release-1.17-hackweek", "main", "release-1"} {
		if _, ok := ParseBranch(input); ok {
			t.Errorf("%q was read as a release branch", input)
		}
	}
}

func TestLinesCompareByNumber(t *testing.T) {
	// As strings, release-1.9 sorts after release-1.24.
	if CompareLines(Line{Major: 1, Minor: 9}, Line{Major: 1, Minor: 24}) >= 0 {
		t.Error("1.9 should come before 1.24")
	}

	if CompareLines(Line{Major: 1, Minor: 0}, Line{Major: 0, Minor: 6}) <= 0 {
		t.Error("1.0 should come after 0.6")
	}
}

func TestVersionsKnowTheirKindAndNames(t *testing.T) {
	minor := Version{Major: 1, Minor: 24}
	patch := Version{Major: 1, Minor: 24, Patch: 1}

	if minor.Kind() != Minor || patch.Kind() != Patch {
		t.Errorf("kinds were %s and %s", minor.Kind(), patch.Kind())
	}

	if minor.Tag() != "v1.24.0" || minor.Line().Branch() != "release-1.24" {
		t.Errorf("1.24.0 gave tag %s on branch %s", minor.Tag(), minor.Line().Branch())
	}

	if minor.NextPatch() != patch {
		t.Errorf("the patch after 1.24.0 was %s", minor.NextPatch())
	}
}
