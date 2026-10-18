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
				writeProfile(t, test.profile, test.yaml)
			}

			// A gh that says it ran, so a refusal that comes too late shows.
			output, err := resetRehearsal(t, test.profile, "echo gh ran >&2\nexit 99\n")
			if err == nil || !strings.Contains(string(output), test.refusal) || strings.Contains(string(output), "gh ran") {
				t.Errorf("reset-rehearsal.sh %s exited with %v:\n%s", test.profile, err, output)
			}
		})
	}
}

// TestResetRehearsalRefusesARepositoryThatIsNotTheFork runs the reset script
// for a user whose repository of the rehearsal's name is a fork of something
// else. It must stop there, before it lists or deletes anything.
func TestResetRehearsalRefusesARepositoryThatIsNotTheFork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	writeProfile(t, "rehearsal", "name: rehearsal\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n")

	output, err := resetRehearsal(t, "rehearsal", `case "$*" in
"api user --jq .login") echo me ;;
"api repos/me/rancher-desktop-rehearsal --jq .parent.full_name") echo someone/rancher-desktop-rehearsal ;;
*) echo "gh ran: $*" >&2; exit 99 ;;
esac
`)

	refusal := "me/rancher-desktop-rehearsal is not a fork of example-org/rancher-desktop-rehearsal"
	if err == nil || !strings.Contains(string(output), refusal) || strings.Contains(string(output), "gh ran") {
		t.Errorf("reset-rehearsal.sh rehearsal exited with %v:\n%s", err, output)
	}
}

// writeProfile writes a profile where the tool looks for it.
func writeProfile(t *testing.T, profile, yaml string) {
	t.Helper()

	dir, err := ProfileDir(profile)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resetRehearsal runs the reset script with a gh whose body is the given
// shell, first on PATH.
func resetRehearsal(t *testing.T, profile, gh string) ([]byte, error) {
	t.Helper()

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+gh), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return exec.CommandContext(t.Context(), "bash", "reset-rehearsal.sh", profile).CombinedOutput()
}
