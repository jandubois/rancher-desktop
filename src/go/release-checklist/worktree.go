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

// worktreePath is where a worktree the release makes goes. An action plans the
// commands it will run in the worktree before anything creates it, so the path
// comes from the release and not from the checkout.
func worktreePath(run *Run, name string) (string, error) {
	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return "", err
	}

	return filepath.Join(cache, name), nil
}

// worktreeAt checks a commit of a clone out at a path under the release's
// cache directory. The clone never changes branch, so a failed action cannot
// leave it on a bump branch. An empty clone is the one the tool runs in.
func worktreeAt(ctx context.Context, run *Run, clone, path, commit string) error {
	// A worktree whose directory somebody deleted by hand stays registered,
	// and the registration alone blocks a new worktree at the same path.
	if _, err := run.Tools.runIn(ctx, clone, "git", "worktree", "prune"); err != nil {
		return fmt.Errorf("pruning worktrees: %w", err)
	}

	// A worktree left by a failed attempt still holds that attempt's changes,
	// staged or not, and the next commit would take them along. The tool owns
	// the worktree, so it starts every attempt from the planned commit alone.
	if _, err := os.Stat(path); err == nil {
		if _, err := run.Tools.runIn(ctx, path, "git", "checkout", "--force", "--detach", commit); err != nil {
			return fmt.Errorf("checking out %s in %s: %w", commit, path, err)
		}

		if _, err := run.Tools.runIn(ctx, path, "git", "clean", "-d", "--force"); err != nil {
			return fmt.Errorf("cleaning %s: %w", path, err)
		}

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), cachePermissions); err != nil {
		return err
	}

	if _, err := run.Tools.runIn(ctx, clone, "git", "worktree", "add", "--detach", path, commit); err != nil {
		return fmt.Errorf("adding a worktree at %s: %w", path, err)
	}

	return nil
}
