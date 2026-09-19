// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"path/filepath"
)

// storeDir is the directory the tool keeps its own files under, inside the
// operating system's config and cache directories.
const storeDir = "rancher-desktop-release"

// ProfileDir is where one profile keeps the files that belong to it: the
// profile itself, the user's settings for it, and the steps confirmed by
// hand. Keying by profile means a rehearsal on a fork cannot mark a step of
// the real release done.
func ProfileDir(profile string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, storeDir, profile), nil
}
