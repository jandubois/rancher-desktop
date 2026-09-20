// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// settingsFile holds what a step needs from this machine: the clones it works
// in, and the accounts it signs in with. The profile names the release's own
// resources and goes into the repository, so one user's paths belong here.
const settingsFile = "settings.yaml"

// Settings are the paths and accounts that belong to whoever runs the release.
type Settings struct {
	// DocsClone is the clone of the documentation repository a step makes its
	// worktree in. The clone never changes branch.
	DocsClone string `yaml:"docsClone"`
}

// SettingsPath is where a profile keeps its settings.
func SettingsPath(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, settingsFile), nil
}

// LoadSettings reads a profile's settings. Most steps need none, so a missing
// file gives empty settings, and the step that wants one says which setting it
// is missing and where to write it.
func LoadSettings(profile string) (Settings, error) {
	path, err := SettingsPath(profile)
	if err != nil {
		return Settings{}, err
	}

	return settingsAt(path)
}

// settingsAt reads the settings kept in one file.
func settingsAt(path string) (Settings, error) {
	settings := Settings{}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}

	if err != nil {
		return settings, err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	// A file of nothing but comments decodes to no document, which is the
	// same as having none.
	if err := decoder.Decode(&settings); err != nil && !errors.Is(err, io.EOF) {
		return Settings{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return settings, nil
}

// docsCloneDir is the clone a documentation step works in, or a line naming
// the setting to write and the file to write it in.
func docsCloneDir(run *Run) (string, error) {
	if run.Settings.DocsClone != "" {
		return run.Settings.DocsClone, nil
	}

	path, err := SettingsPath(run.Profile.Name)
	if err != nil {
		return "", err
	}

	return "", fmt.Errorf("%s names no docsClone, the path of your clone of %s "+
		"that this step makes its worktree in", path, run.Profile.GitHub.DocsRepo)
}
