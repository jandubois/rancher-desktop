// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsFrom writes a settings file and reads it back.
func settingsFrom(t *testing.T, content string) (Settings, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), settingsFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return settingsAt(path)
}

func TestSettingsReadTheClonePath(t *testing.T) {
	settings, err := settingsFrom(t, "docsClone: /home/me/git/docs\n")
	if err != nil {
		t.Fatal(err)
	}

	if settings.DocsClone != "/home/me/git/docs" {
		t.Errorf("the settings read %+v", settings)
	}
}

// Most steps need no settings, and a file holding nothing but comments is
// somebody who started one, so neither is an error.
func TestSettingsWithNothingInThemAreEmpty(t *testing.T) {
	for what, content := range map[string]string{
		"a file of comments": "# which clone the docs steps work in\n",
		"an empty file":      "",
	} {
		settings, err := settingsFrom(t, content)
		if err != nil {
			t.Errorf("%s: %v", what, err)

			continue
		}

		if settings.DocsClone != "" {
			t.Errorf("%s gave %+v", what, settings)
		}
	}

	settings, err := settingsAt(filepath.Join(t.TempDir(), settingsFile))
	if err != nil || settings.DocsClone != "" {
		t.Errorf("no file at all gave %+v, %v", settings, err)
	}
}

// A misspelled setting would otherwise be read as one nobody wrote, and the
// step would ask for a path the file already holds.
func TestSettingsRefuseAFieldNobodyReads(t *testing.T) {
	_, err := settingsFrom(t, "docsclone: /home/me/git/docs\n")
	if err == nil || !strings.Contains(err.Error(), "docsclone") {
		t.Errorf("a misspelled setting gave %v", err)
	}
}
