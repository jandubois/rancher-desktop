// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// cachePermissions keeps the release's downloads and worktrees readable only
// where the user's own files are.
const cachePermissions = 0o755

// CacheDir is where one release keeps its downloads and worktrees. It is
// keyed by profile and version, and nothing here removes it, so a worktree
// that needs its own yarn install pays for it once instead of once per
// attempt.
func CacheDir(profile string, version Version) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, storeDir, profile, version.String()), nil
}

// worktreeAt checks a commit out under the release's cache directory and
// returns the path. The user's own clone never changes branch, so a failed
// action cannot leave it on a bump branch.
func worktreeAt(ctx context.Context, run *Run, name, commit string) (string, error) {
	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return "", err
	}

	path := filepath.Join(cache, name)

	// A worktree whose directory somebody deleted by hand stays registered,
	// and the registration alone blocks a new worktree at the same path.
	if _, err := run.Tools.run(ctx, "git", "worktree", "prune"); err != nil {
		return "", fmt.Errorf("pruning worktrees: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if _, err := run.Tools.runIn(ctx, path, "git", "checkout", "--detach", commit); err != nil {
			return "", fmt.Errorf("checking out %s in %s: %w", commit, path, err)
		}

		return path, nil
	}

	if err := os.MkdirAll(cache, cachePermissions); err != nil {
		return "", err
	}

	if _, err := run.Tools.run(ctx, "git", "worktree", "add", "--detach", path, commit); err != nil {
		return "", fmt.Errorf("adding a worktree at %s: %w", path, err)
	}

	return path, nil
}
