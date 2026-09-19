// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"io"
)

// printHeader names the release being driven, so a run against a fork profile
// cannot be mistaken for one against the real thing.
func printHeader(out io.Writer, release *Release, profile *Profile) {
	state := release.Kind
	if release.Published {
		state += ", published"
	}

	fmt.Fprintf(out, "%s · %s · %s\n", release.Version, state, profile.Name)

	for _, warning := range release.Warnings {
		fmt.Fprintf(out, "  ! %s\n", warning)
	}
}
