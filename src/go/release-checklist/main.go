// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// release-checklist drives a Rancher Desktop release. It finds the release in
// progress, checks each step it ships, and prints the checklist.
//
// Usage:
//
//	yarn release [--profile <name>]
//
// See README.md for what it checks and what it changes.
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
	stepID := flag.String("run", "",
		"run one step's automation, by its number in the checklist")
	flag.Parse()

	profile, err := LoadProfile(*profileName)
	if err != nil {
		return err
	}

	run, err := refresh(ctx, profile)
	if err != nil {
		return err
	}

	if *stepID != "" {
		step, found := findStep(*stepID)
		if !found {
			return fmt.Errorf("the checklist has no step %s", *stepID)
		}

		return RunAction(ctx, step, run, os.Stdin, os.Stdout)
	}

	printStatus(ctx, os.Stdout, run)

	return nil
}

// refresh reads everything the checklist is derived from: which release is in
// progress, and the refs the steps check against.
func refresh(ctx context.Context, profile *Profile) (*Run, error) {
	repo := newRepository(ctx, profile.GitHub.Repo, tools{})

	release, err := chooseRelease(ctx, repo)
	if err != nil {
		return nil, err
	}

	refs, err := repo.Refs(ctx)
	if err != nil {
		return nil, err
	}

	run := newRun(release, profile, repo)
	run.Refs = refs

	return run, nil
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
