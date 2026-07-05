// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const translationsDir = "pkg/rancher-desktop/assets/translations"

// repoRoot returns the repository root by walking up from the current
// directory looking for the translations directory. Nested package.json
// files (bats/, sudo-prompt/) make that marker ambiguous.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, translationsDir)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root (no %s directory found)", translationsDir)
		}
		dir = parent
	}
}

// translationsPath returns the absolute path to a file in the translations directory.
func translationsPath(root, filename string) string {
	return filepath.Join(root, translationsDir, filename)
}

// filePerm is the mode for every file the tool writes.
const filePerm = 0o644

// writeFileAtomic writes data to a temp file in the target directory and
// renames it into place, so an interrupted write cannot truncate the target.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(filePerm); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
