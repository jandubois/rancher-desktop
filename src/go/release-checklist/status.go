// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
)

// printStatus writes the whole checklist as plain text: the release being
// driven, then one line per step with its state and what the check found.
func printStatus(ctx context.Context, out io.Writer, run *Run) {
	printHeader(out, run.Release, run.Profile)

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	for _, step := range checklist {
		status := run.Status(ctx, step)
		fmt.Fprintf(table, "  %s\t%s\t%s\t%s\n", step.ID, step.Title, status.State, status.Detail)
	}

	if err := table.Flush(); err != nil {
		fmt.Fprintf(out, "writing the checklist: %v\n", err)
	}
}

// printHeader names the release being driven, so a run against a fork profile
// cannot be mistaken for one against the real thing.
func printHeader(out io.Writer, release *Release, profile *Profile) {
	fmt.Fprintf(out, "%s · %s · %s\n", release.Version, release.State(), profile.Name)

	for _, warning := range release.Warnings {
		fmt.Fprintf(out, "  ! %s\n", warning)
	}
}
