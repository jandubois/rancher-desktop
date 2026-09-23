// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// updateREADME makes `go test -update` rewrite the README's generated parts
// instead of checking them.
var updateREADME = flag.Bool("update", false, "rewrite the README's generated parts from the step definitions")

func TestREADMEDescribesTheStepsItShips(t *testing.T) {
	const path = "README.md"

	readme, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	text := string(readme)

	for _, region := range readmeRegions {
		before, rest, found := strings.Cut(text, region.Begin)
		if !found {
			t.Fatalf("%s should hold the line %q", path, region.Begin)
		}

		current, after, found := strings.Cut(rest, region.End)
		if !found {
			t.Fatalf("%s should hold the line %q after %q", path, region.End, region.Begin)
		}

		generated := region.Render(checklist)

		if !*updateREADME && strings.TrimSpace(current) != strings.TrimSpace(generated) {
			t.Errorf("%s below %q no longer describes the steps; run `go test -update` and read the change",
				path, region.Begin)
		}

		text = before + region.Begin + "\n\n" + generated + "\n" + region.End + after
	}

	if *updateREADME {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
