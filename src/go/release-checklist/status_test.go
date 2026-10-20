// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestTheStatusNamesTheReleaseAndEachStepsState(t *testing.T) {
	inACacheOfItsOwn(t)

	run := checklistRun(t, Version{Major: 1, Minor: 25, Patch: 1}, map[Line]string{testRelease.Line(): testHead}, &fakeTools{})
	run.Release.Warnings = []string{"release-1.25 has commits past v1.25.1"}

	var out bytes.Buffer
	printStatus(t.Context(), &out, run)
	printed := out.String()

	if first, want := strings.SplitN(printed, "\n", 2)[0], "1.25.1 · patch · test"; first != want {
		t.Errorf("the first line is %q, not %q:\n%s", first, want, printed)
	}

	if want := "  ! release-1.25 has commits past v1.25.1\n"; !strings.Contains(printed, want) {
		t.Errorf("the output does not show the warning %q:\n%s", want, printed)
	}

	if !regexp.MustCompile(`(?m)^  1 +` + releaseBranch.Title + ` +skipped\b`).MatchString(printed) {
		t.Errorf("step 1 does not read as skipped:\n%s", printed)
	}
}
