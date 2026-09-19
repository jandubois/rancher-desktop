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

// updateReference rewrites the README's step reference instead of checking
// it: `go test -update`.
var updateReference = flag.Bool("update", false, "rewrite the README step reference from the step definitions")

func TestREADMEDescribesTheStepsItShips(t *testing.T) {
	const path = "README.md"

	readme, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	overview, reference, found := strings.Cut(string(readme), referenceMarker)
	if !found {
		t.Fatalf("%s has no step reference; it should hold the line %q", path, referenceMarker)
	}

	generated := stepReference(checklist)

	if *updateReference {
		updated := overview + referenceMarker + "\n\n" + generated
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	if strings.TrimSpace(reference) != strings.TrimSpace(generated) {
		t.Errorf("%s no longer describes the steps; run `go test -update` and read the change", path)
	}
}
