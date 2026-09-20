// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"strings"
)

// referenceMarker opens the generated part of the README. Everything after it
// is rendered from the step definitions, and a test fails when the two
// disagree, so a change to a step cannot quietly change what the README
// promises.
const referenceMarker = "<!-- The reference below is generated from the step definitions. -->"

// stepReference renders the checklist for the README. It keeps the
// placeholders, so the reference describes every release rather than the one
// in progress.
func stepReference(steps []*Step) string {
	var out strings.Builder

	for _, step := range steps {
		fmt.Fprintf(&out, "### %s. %s\n\n", step.ID, step.Title)
		fmt.Fprintf(&out, "- **Applies to:** %s\n", step.Doc.Applies)
		fmt.Fprintf(&out, "- **Done when:** %s\n", step.Doc.Check)
		fmt.Fprintf(&out, "- **Waits for:** %s\n", step.Doc.Precondition)
		fmt.Fprintf(&out, "- **Reaches:** %s\n", reaches(step))
		fmt.Fprintf(&out, "- **Runs:** %s\n", runs(step))
		fmt.Fprintf(&out, "- **Gathers:** %s\n\n", gathers(step))
		fmt.Fprintf(&out, "%s\n\n", step.Doc.Instructions)
	}

	return strings.TrimRight(out.String(), "\n") + "\n"
}

// runs says what the step's automation does. The tool shows every command it
// would run, filled in with this release's values, before it runs any of
// them. The wording suits both places it appears: under the instructions in
// the README, and in the dashboard pane that keeps them behind a key.
func runs(step *Step) string {
	if step.Action == nil {
		return "nothing. Follow the instructions."
	}

	return step.Action.Title + "."
}

// gathers says what reference material the step writes for whoever does its
// manual work.
func gathers(step *Step) string {
	if step.Facts == nil {
		return "nothing. The instructions are all the step needs."
	}

	return fmt.Sprintf("%s, written to %s under the release's cache directory.",
		step.Facts.Title, step.Facts.File)
}

// reaches names the systems a step touches, so a reader can see what it can
// reach before running it.
func reaches(step *Step) string {
	if len(step.Needs) == 0 {
		return "nothing outside this machine."
	}

	named := make([]string, 0, len(step.Needs))
	for _, resource := range step.Needs {
		named = append(named, resource.Name)
	}

	return strings.Join(named, ", ") + "."
}
