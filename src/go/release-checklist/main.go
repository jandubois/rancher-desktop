// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// release-checklist drives a Rancher Desktop release. It finds the release in
// progress, checks every step of the release process, and offers the steps
// that are available but not done.
//
// Usage:
//
//	yarn release [--profile <name>]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	profileName := flag.String("profile", productionName,
		"the profile naming the resources to release to")
	flag.Parse()

	profile, err := LoadProfile(*profileName)
	if err != nil {
		return err
	}

	release, err := chooseRelease(ctx, newRepository(ctx, profile.GitHub.Repo, tools{}))
	if err != nil {
		return err
	}

	printHeader(os.Stdout, release, profile)

	return nil
}

// chooseRelease is the release VERSION names, or, with VERSION unset, the one
// the repository says is in progress. The override releases a patch on an
// older line, which the rule would never pick.
func chooseRelease(ctx context.Context, facts repoFacts) (*Release, error) {
	override := os.Getenv("VERSION")
	if override == "" {
		return Detect(ctx, facts)
	}

	version, err := ParseVersion(override)
	if err != nil {
		return nil, fmt.Errorf("VERSION: %w", err)
	}

	return &Release{Version: version, Kind: version.Kind()}, nil
}
