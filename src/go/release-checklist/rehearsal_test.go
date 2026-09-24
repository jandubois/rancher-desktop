// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResetRehearsalRefusesTheRealRelease runs the reset script against
// profiles that name the real release. It must refuse before gh runs, and it
// has to find the profile where the tool itself looks for it.
func TestResetRehearsalRefusesTheRealRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	tests := map[string]struct{ profile, yaml, refusal string }{
		"the production profile": {
			profile: "Production",
			refusal: "resets rehearsals only",
		},
		"the release repository": {
			profile: "rehearsal",
			yaml:    "name: rehearsal\ngithub:\n  repo: rancher-sandbox/rancher-desktop\n",
			refusal: "names rancher-sandbox/rancher-desktop",
		},
		"the documentation repository": {
			profile: "rehearsal",
			yaml: "name: rehearsal\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n" +
				"  docsRepo: Rancher-Sandbox/docs.rancherdesktop.io\n",
			refusal: "names Rancher-Sandbox/docs.rancherdesktop.io",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_CACHE_HOME", "")

			if test.yaml != "" {
				dir, err := ProfileDir(test.profile)
				if err != nil {
					t.Fatal(err)
				}

				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(test.yaml), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// A gh that says it ran, so a refusal that comes too late shows.
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho gh ran >&2\nexit 99\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := exec.CommandContext(t.Context(), "bash", "reset-rehearsal.sh", test.profile).CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.refusal) || strings.Contains(string(output), "gh ran") {
				t.Errorf("reset-rehearsal.sh %s exited with %v:\n%s", test.profile, err, output)
			}
		})
	}
}
