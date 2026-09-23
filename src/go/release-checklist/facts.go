// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// factsFile is what the release notes facts are written as, under the
// release's cache directory.
const factsFile = "release-notes-facts.md"

// dependenciesFile lists the version of every tool a build downloads.
const dependenciesFile = "pkg/rancher-desktop/assets/dependencies.yaml"

// bundledUtility is a tool the release notes name. The key is its entry in
// dependencies.yaml and the name is what a user calls it, and the two differ
// for most of them.
type bundledUtility struct{ key, name string }

// bundledUtilities are the tools the notes list, in the order the notes list
// them. Everything else in dependencies.yaml builds Rancher Desktop rather
// than shipping inside it, so no user reads its version. nerdctl and
// containerd come from the guest ISO, so they are not here.
var bundledUtilities = []bundledUtility{
	{"dockerCLI", "docker"},
	{"dockerCompose", "docker-compose"},
	{"dockerBuildx", "docker-buildx"},
	{"dockerProvidedCredentialHelpers", "docker-credential-helpers"},
	{"helm", "helm"},
	{"kuberlr", "kuberlr"},
	{"trivy", "trivy"},
	{"ECRCredentialHelper", "amazon-ecr-credential-helper"},
	{"spinCLI", "spin"},
	{"spinShim", "spin-shim"},
	{"spinOperator", "spin-operator"},
	{"certManager", "cert-manager"},
}

// firstContribution reads a line of GitHub's New Contributors list.
var firstContribution = regexp.MustCompile(`^\* @(\S+) made their first contribution in (\S+)`)

// contributor is somebody GitHub named as making their first contribution,
// and what the repository's whole history says about that.
type contributor struct {
	Login string
	PR    string
	// Earlier is true when the author already has a commit from before the
	// previous release, which makes GitHub's claim wrong.
	Earlier bool
}

// dependency is one entry of dependencies.yaml. The version is a node
// because an entry such as the guest ISO carries a mapping there.
type dependency struct {
	Version yaml.Node `yaml:"version"`
}

// dependencyVersions is the version of each tool in a dependencies.yaml. An
// entry whose version is a mapping rather than a string, such as the guest
// ISO, has no single version and is left out.
func dependencyVersions(content []byte) (map[string]string, error) {
	var file map[string]*dependency

	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("reading %s: %w", dependenciesFile, err)
	}

	versions := make(map[string]string, len(file))

	for name, entry := range file {
		if entry != nil && entry.Version.Kind == yaml.ScalarNode {
			versions[name] = entry.Version.Value
		}
	}

	return versions, nil
}

// previousRelease is the release the notes compare against: the first
// release of the previous line for a minor, and the release before it on the
// same line for a patch. A minor starts from the previous line's first
// release because the work that reached main while that line was being
// patched ships in this one. Versions with no tag were burned rather than
// released, so the search passes over them.
func previousRelease(version Version, refs *Refs) (Version, bool) {
	if refs.KindOf(version) == Patch {
		for patch := version.Patch - 1; patch >= 0; patch-- {
			candidate := Version{Major: version.Major, Minor: version.Minor, Patch: patch}
			if _, tagged := refs.Tags[candidate]; tagged {
				return candidate, true
			}
		}

		return Version{}, false
	}

	for minor := version.Minor - 1; minor >= 0; minor-- {
		if first, found := refs.LowestTag(Line{Major: version.Major, Minor: minor}); found {
			return first, true
		}
	}

	return Version{}, false
}

// utilityChanges is what each bundled utility did between the two releases,
// as the notes report it: the ones that moved, then the ones that stayed.
func utilityChanges(before, after map[string]string) (moved, same []string) {
	for _, utility := range bundledUtilities {
		was, had := before[utility.key]
		now, has := after[utility.key]

		if !had && !has {
			// Neither release bundled it, so the notes say nothing of it.
			continue
		}

		switch {
		case !has:
			moved = append(moved, fmt.Sprintf("* %s is gone from %s", utility.name, dependenciesFile))
		case !had:
			moved = append(moved, fmt.Sprintf("* %s `%s` is new", utility.name, now))
		case was != now:
			moved = append(moved, fmt.Sprintf("* %s `%s` → `%s`", utility.name, was, now))
		default:
			same = append(same, fmt.Sprintf("* %s `%s`", utility.name, now))
		}
	}

	return moved, same
}

// newContributors is everybody GitHub calls a first-time contributor,
// checked against the history before the previous release. GitHub reads only
// the pull requests between the two releases, so somebody who contributed
// years ago and came back counts as new there.
func newContributors(ctx context.Context, run *Run, previous Version, target string) ([]contributor, error) {
	drafted, err := run.Repo.GeneratedNotes(ctx, run.Release.Tag(), previous.Tag(), target)
	if err != nil {
		return nil, err
	}

	named := parseNewContributors(drafted)
	if len(named) == 0 {
		return nil, nil
	}

	until, err := run.Repo.CommitDate(ctx, previous.Tag())
	if err != nil {
		return nil, err
	}

	for index, person := range named {
		earlier, err := run.Repo.CommittedBefore(ctx, person.Login, until)
		if err != nil {
			return nil, err
		}

		named[index].Earlier = earlier
	}

	return named, nil
}

// parseNewContributors reads the logins and pull requests out of GitHub's
// drafted notes. The list runs from its own heading to the next one.
func parseNewContributors(drafted string) []contributor {
	var named []contributor

	inList := false

	for line := range strings.SplitSeq(drafted, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") {
			inList = strings.EqualFold(trimmed, "## New Contributors")

			continue
		}

		if match := firstContribution.FindStringSubmatch(trimmed); inList && match != nil {
			named = append(named, contributor{Login: match[1], PR: match[2]})
		}
	}

	return named
}

// gatherNotesFacts is the material the notes are written from: how the
// bundled utilities moved, who is contributing for the first time, and the
// two links the notes close with. The writer supplies the prose and the
// judgment.
func gatherNotesFacts(ctx context.Context, run *Run) (string, error) {
	version := run.Release.Version
	branch := run.Release.Branch()

	if _, ok := run.Refs.Branches[version.Line()]; !ok {
		return "", fmt.Errorf("%s has no %s branch to read the release from", run.Repo.repo, branch)
	}

	previous, found := previousRelease(version, run.Refs)
	if !found {
		return "", fmt.Errorf("%s has no release before %s to compare with", run.Repo.repo, version)
	}

	utilities, err := utilitySection(ctx, run, previous, branch)
	if err != nil {
		return "", err
	}

	named, err := newContributors(ctx, run, previous, branch)
	if err != nil {
		return "", err
	}

	changelog, err := changelogSection(ctx, run, previous)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		"# Release notes facts for " + version.String(),
		fmt.Sprintf("Read from %s on %s, comparing %s with %s.",
			run.Repo.repo, time.Now().Local().Format(time.DateOnly), previous.Tag(), branch),
		"nerdctl and containerd ship in the guest ISO, so neither version is " +
			"here; containerd's is readable only from a running VM. The CVEs a " +
			"bump fixes take judgment, so they are not here either.",
		utilities,
		contributorSection(named, previous),
		changelog,
	}, "\n\n") + "\n", nil
}

// utilitySection lists how the bundled utilities moved, between the previous
// release's tag and the release branch. The tag wins over the previous
// release's published notes, which can disagree with it when a bump arrived
// after the prose was written.
func utilitySection(ctx context.Context, run *Run, previous Version, branch string) (string, error) {
	was, err := run.Repo.FileAtRef(ctx, previous.Tag(), dependenciesFile)
	if err != nil {
		return "", err
	}

	before, err := dependencyVersions(was)
	if err != nil {
		return "", err
	}

	is, err := run.Repo.FileAtRef(ctx, branch, dependenciesFile)
	if err != nil {
		return "", err
	}

	after, err := dependencyVersions(is)
	if err != nil {
		return "", err
	}

	moved, same := utilityChanges(before, after)

	section := []string{fmt.Sprintf("## Updates to Bundled Utilities (from Rancher Desktop %s)", previous)}

	if len(moved) == 0 {
		section = append(section, "No bundled utility moved.")
	} else {
		section = append(section, strings.Join(moved, "\n"))
	}

	if len(same) > 0 {
		section = append(section, "Unchanged:\n"+strings.Join(same, "\n"))
	}

	return strings.Join(section, "\n\n"), nil
}

// contributorSection credits the first-time contributors and shows what the
// history said about each name, so the writer can see the check that put it
// there or left it out.
func contributorSection(named []contributor, previous Version) string {
	section := []string{"## New Contributors"}

	logins := make([]string, 0, len(named))
	evidence := make([]string, 0, len(named))

	for _, person := range named {
		if person.Earlier {
			evidence = append(evidence, fmt.Sprintf(
				"* @%s already had a commit before %s, so they are not new: %s",
				person.Login, previous.Tag(), person.PR))

			continue
		}

		logins = append(logins, "@"+person.Login)
		evidence = append(evidence, fmt.Sprintf("* @%s has no commit before %s: %s",
			person.Login, previous.Tag(), person.PR))
	}

	switch {
	case len(named) == 0:
		section = append(section, "GitHub named nobody as contributing for the first time.")
	case len(logins) == 1:
		section = append(section, fmt.Sprintf("Thank you to our new contributor: %s!", logins[0]))
	case len(logins) > 1:
		section = append(section, fmt.Sprintf("Thank you to our new contributors: %s!", nameList(logins)))
	default:
		section = append(section, "Everybody GitHub named has contributed before.")
	}

	if len(evidence) > 0 {
		section = append(section, strings.Join(evidence, "\n"))
	}

	return strings.Join(section, "\n\n")
}

// changelogSection is the paragraph the notes close with: the compare link
// and the release's milestone.
func changelogSection(ctx context.Context, run *Run, previous Version) (string, error) {
	repo := run.Repo.repo
	tag := run.Release.Tag()

	compare := fmt.Sprintf("https://github.com/%s/compare/%s...%s", repo, previous.Tag(), tag)

	title := milestoneTitle(run.Release)

	number, err := run.Repo.Milestone(ctx, title)
	if err != nil {
		return "", err
	}

	if number == 0 {
		return fmt.Sprintf("## Changelog\n\n"+
			"The full version changelog, from %s, can be found using "+
			"[GitHub compare](%s). %s has no milestone called %q yet, so the "+
			"notes cannot link one.", previous.Tag(), compare, repo, title), nil
	}

	return fmt.Sprintf("## Changelog\n\n"+
		"The full version changelog, from %s, can be found using "+
		"[GitHub compare](%s) and the details of the release can be found in "+
		"the [%s](https://github.com/%s/milestone/%d?closed=1) milestone.",
		previous.Tag(), compare, tag, repo, number), nil
}

// milestoneTitle is what the release's milestone is called. A minor release
// shares its line's milestone, because the line opens with it; a patch has
// one of its own.
func milestoneTitle(release *Release) string {
	if release.Kind == Minor {
		return release.Version.Line().String()
	}

	return release.Version.String()
}

// nameList joins names the way a sentence does.
func nameList(names []string) string {
	if len(names) < 3 {
		return strings.Join(names, " and ")
	}

	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}
