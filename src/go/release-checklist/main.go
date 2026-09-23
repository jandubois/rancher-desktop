// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// release-checklist drives a Rancher Desktop release. It finds the release in
// progress, checks each step it ships, and shows the checklist.
//
// Usage:
//
//	yarn release [--profile <name>] [--status] [--run <step>]
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
	text := flag.Bool("status", false,
		"print the checklist and exit, instead of opening the dashboard")
	flag.Parse()

	profile, err := LoadProfile(*profileName)
	if err != nil {
		return err
	}

	switch {
	case *stepID != "":
		return runStep(ctx, profile, *stepID)
	case *text:
		return printChecklist(ctx, profile)
	default:
		return showDashboard(ctx, profile)
	}
}

// runStep runs one step's automation without opening the dashboard, which is
// how a step is driven from a script.
func runStep(ctx context.Context, profile *Profile, stepID string) error {
	step, found := findStep(stepID)
	if !found {
		return fmt.Errorf("the checklist has no step %s", stepID)
	}

	run, err := refresh(ctx, profile)
	if err != nil {
		return err
	}

	return RunAction(ctx, step, run, os.Stdin, os.Stdout)
}

// printChecklist writes the whole checklist as plain text, for a terminal the
// dashboard cannot draw in and for pasting into a report.
func printChecklist(ctx context.Context, profile *Profile) error {
	run, err := refresh(ctx, profile)
	if err != nil {
		return err
	}

	printStatus(ctx, os.Stdout, run)

	return nil
}

// refresh reads everything the checklist is derived from: which release is in
// progress, the refs the steps check against, the steps somebody has marked
// done, and the settings naming this machine's clones.
func refresh(ctx context.Context, profile *Profile) (*Run, error) {
	repo := newRepository(ctx, "", profile.GitHub.Repo, tools{})

	release, err := chooseRelease(ctx, repo)
	if err != nil {
		return nil, err
	}

	refs, err := repo.Refs(ctx)
	if err != nil {
		return nil, err
	}

	marked, err := loadConfirmations(profile.Name)
	if err != nil {
		return nil, err
	}

	settings, err := LoadSettings(profile.Name)
	if err != nil {
		return nil, err
	}

	run := newRun(release, profile, repo)
	run.Refs = refs
	run.Confirmations = marked
	run.Settings = settings

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

	finished, err := facts.TagInMain(ctx, version.Tag())
	if err != nil {
		return nil, err
	}

	return &Release{Version: version, Kind: version.Kind(), Finished: finished}, nil
}
