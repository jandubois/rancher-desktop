// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// docsVersionDir holds one file per release, each listing the versions that
// release bundles.
const docsVersionDir = "docs/bundled-utilities-version-info"

// docsReferencePage imports the version files and shows them in a table.
const docsReferencePage = "docs/references/bundled-utilities.md"

// utilityLine is how a version file names one utility. The site renders the
// file inside a table cell, so every line ends with a break.
var utilityLine = regexp.MustCompile(`^(\S+): (\S+) <br/>$`)

// versionFileName names the file listing what a release bundles.
func versionFileName(version Version) string { return "v" + version.String() + ".md" }

// versionFilePath is that file's path in the documentation repository.
func versionFilePath(version Version) string {
	return docsVersionDir + "/" + versionFileName(version)
}

// docsUtilities is step 7a. The bundled utility versions are the one part of
// the documentation a release always changes, and they are already written
// down in dependencies.yaml, so the tool reads them rather than asking
// anybody to copy them across.
var docsUtilities = &Step{
	ID:    "7a",
	Title: "Docs: bundled utilities",
	Kinds: []Kind{Minor},
	Needs: []*Resource{githubRepo, githubDocsRepo},
	Doc: Doc{
		Applies: "Minor releases. A patch ships the documentation its line already has, " +
			"unless a bundled utility moved.",
		Check: "The release branch of your fork of {docsRepo}, or {docsRepo}'s own " +
			"main branch once the documentation is merged, has " +
			"bundled-utilities-version-info/v{version}.md, the reference page " +
			"imports it, and the file lists the versions {version} bundles.",
		Precondition: "gh can push to {docsRepo}, and the release branch exists.",
		Instructions: "Write the bundled utility versions for {version} into the " +
			"documentation:\n\n" +
			"1. Add docs/bundled-utilities-version-info/v{version}.md, one utility " +
			"per line, in the form `name: version <br/>`.\n" +
			"2. Delete the oldest file in that directory, so the page lists three " +
			"releases.\n" +
			"3. In docs/references/bundled-utilities.md, import the new file and add " +
			"its row at the top of the table, and take out the deleted one.\n\n" +
			"Every version but nerdctl's comes from dependencies.yaml on {branch}. " +
			"nerdctl ships in the guest images instead. The Lima image's Makefile " +
			"and the WSL distribution's versions.env each set NERDCTL_VERSION, in " +
			"the image release that dependencies.yaml names.\n\n" +
			"Commit it to a release-{line} branch of your fork of {docsRepo} with a " +
			"sign-off. The rdctl reference and the versioning snapshot go on the " +
			"same branch, and one pull request carries all three.",
	},
	Check:        checkDocsUtilities,
	Precondition: docsUtilitiesReady,
}

// docsUtilitiesReady holds the step until the release has a branch to read
// dependencies.yaml from.
func docsUtilitiesReady(ctx context.Context, run *Run) (Answer, error) {
	return waitFor(ctx, run, releaseBranch), nil
}

// checkDocsUtilities reports whether the documentation lists the versions
// this release bundles.
func checkDocsUtilities(ctx context.Context, run *Run) (Answer, error) {
	version := run.Release.Version
	name := versionFileName(version)

	docs, ref, err := docsLocation(ctx, run)
	if err != nil {
		return Answer{}, err
	}

	where := fmt.Sprintf("%s at %s", docs.repo, ref)

	listed, err := docs.FileAtRef(ctx, ref, versionFilePath(version))
	if errors.Is(err, errNotFound) {
		return Answer{Detail: where + " has no " + name}, nil
	}

	if err != nil {
		return Answer{}, err
	}

	page, err := docs.FileAtRef(ctx, ref, docsReferencePage)
	if errors.Is(err, errNotFound) {
		return Answer{Detail: where + " has no " + docsReferencePage}, nil
	}

	if err != nil {
		return Answer{}, err
	}

	if !strings.Contains(string(page), versionFileName(version)) {
		return Answer{Detail: fmt.Sprintf("%s in %s does not import %s",
			docsReferencePage, where, name)}, nil
	}

	bundled, err := bundledVersions(ctx, run)
	if err != nil {
		return Answer{}, err
	}

	if wrong := utilitiesDiffer(parseVersionFile(listed), bundled); wrong != "" {
		return Answer{Detail: fmt.Sprintf("%s in %s %s", name, where, wrong)}, nil
	}

	return Answer{OK: true, Detail: fmt.Sprintf("%s in %s lists the %d utilities %s bundles",
		name, where, len(bundled), version)}, nil
}

// docsLocation is where the release's documentation is: the release branch on
// the user's fork while the pull request is open, and the documentation
// repository's own main branch once it is merged. Nothing keeps the branch
// after a merge, so a check that read only the fork would show every finished
// release as unfinished.
func docsLocation(ctx context.Context, run *Run) (*repository, string, error) {
	docs := run.Docs(ctx)

	fork, err := docs.Fork(ctx)
	if err != nil {
		return nil, "", err
	}

	branch := run.Release.Branch()

	_, err = fork.BranchHead(ctx, branch)

	switch {
	case err == nil:
		return fork, branch, nil
	case errors.Is(err, errNotFound):
		return docs, defaultBranch, nil
	default:
		return nil, "", err
	}
}

// bundledVersions is every utility the release bundles, by the name the
// documentation calls it. The list is the one the release notes use, so both
// name the same tools.
func bundledVersions(ctx context.Context, run *Run) (map[string]string, error) {
	ref := bundledRef(run)

	dependencies, err := run.Repo.FileAtRef(ctx, ref, dependenciesFile)
	if err != nil {
		return nil, err
	}

	versions, err := dependencyVersions(dependencies)
	if err != nil {
		return nil, err
	}

	bundled := make(map[string]string, len(bundledUtilities)+1)

	for _, utility := range bundledUtilities {
		if version, has := versions[utility.key]; has {
			bundled[utility.name] = version
		}
	}

	nerdctl, err := nerdctlVersion(ctx, run, dependencies)
	if err != nil {
		return nil, err
	}

	bundled["nerdctl"] = nerdctl

	return bundled, nil
}

// bundledRef is where dependencies.yaml says what the release bundles: the
// tag once it is pushed, since that is what shipped, and the release branch
// until then, since the documentation is written before the tag.
func bundledRef(run *Run) string {
	if _, tagged := run.Refs.Tags[run.Release.Version]; tagged {
		return run.Release.Tag()
	}

	return run.Release.Branch()
}

// guestImage is an image a release ships nerdctl inside. The image is built
// from its own repository, and that repository pins the nerdctl it installs.
type guestImage struct {
	// entry names the image in dependencies.yaml.
	entry string
	// pin is the file in the image's repository that sets NERDCTL_VERSION.
	pin string
	// platform names the image in the line reporting a disagreement.
	platform string
}

// guestImages are the images a release ships nerdctl in, one for the Lima
// virtual machine and one for WSL. The documentation lists a single nerdctl,
// so the tool reads both pins and refuses to guess when they differ.
var guestImages = []guestImage{
	{"alpineLimaISO", "Makefile", "the Lima guest image"},
	{"WSLDistro", "versions.env", "the WSL distribution"},
}

// nerdctlPin matches the line each guest image sets its nerdctl version on.
// The Lima image writes a bare version and the WSL distribution writes a tag,
// so the v is optional.
var nerdctlPin = regexp.MustCompile(`(?m)^NERDCTL_VERSION=v?(\S+)\s*$`)

// nerdctlVersion is the nerdctl a release bundles. It ships inside the guest
// images rather than in dependencies.yaml, and each image's repository pins
// the version it installs, so that pin is what the documentation lists.
func nerdctlVersion(ctx context.Context, run *Run, dependencies []byte) (string, error) {
	sources, err := guestImageSources(dependencies)
	if err != nil {
		return "", err
	}

	found := map[string][]string{}

	for _, image := range guestImages {
		source, pinned := sources[image.entry]
		if !pinned {
			return "", fmt.Errorf("%s names no release for %s, so nothing says which nerdctl %s ships",
				dependenciesFile, image.entry, image.platform)
		}

		content, err := fileAtRef(ctx, run.Tools, source.repo, source.tag, image.pin)
		if err != nil {
			return "", fmt.Errorf("reading the nerdctl pin of %s: %w", image.platform, err)
		}

		match := nerdctlPin.FindSubmatch(content)
		if match == nil {
			return "", fmt.Errorf("%s at %s of %s sets no NERDCTL_VERSION",
				image.pin, source.tag, source.repo)
		}

		version := string(match[1])
		found[version] = append(found[version], image.platform)
	}

	pinned := slices.Sorted(maps.Keys(found))

	if len(pinned) != 1 {
		return "", fmt.Errorf("the guest images bundle different versions of nerdctl: %s",
			strings.Join(describeVersions(found), ", "))
	}

	return pinned[0], nil
}

// describeVersions says which images pinned which version, for the line that
// reports a disagreement.
func describeVersions(found map[string][]string) []string {
	described := make([]string, 0, len(found))
	for _, version := range slices.Sorted(maps.Keys(found)) {
		described = append(described, fmt.Sprintf("%s in %s", version, nameList(found[version])))
	}

	return described
}

// releaseSource is the repository and tag a download URL names.
type releaseSource struct{ repo, tag string }

// guestImageSources is the repository and tag each guest image was released
// from, read out of the download URL dependencies.yaml already has, so no step
// has to name the guest image repositories.
func guestImageSources(dependencies []byte) (map[string]releaseSource, error) {
	var file map[string]*struct {
		Assets []struct {
			URL string `yaml:"url"`
		} `yaml:"assets"`
	}

	if err := yaml.Unmarshal(dependencies, &file); err != nil {
		return nil, fmt.Errorf("reading %s: %w", dependenciesFile, err)
	}

	sources := map[string]releaseSource{}

	for name, entry := range file {
		if entry == nil {
			continue
		}

		for _, asset := range entry.Assets {
			if source, ok := parseReleaseURL(asset.URL); ok {
				sources[name] = source

				break
			}
		}
	}

	return sources, nil
}

// releasePrefix begins the download URL of a file attached to a GitHub
// release.
const releasePrefix = "https://github.com/"

// releaseMiddle separates the repository from the tag in that URL.
const releaseMiddle = "/releases/download/"

// parseReleaseURL reads the repository and tag out of a GitHub release
// download URL. An asset hosted anywhere else names neither.
func parseReleaseURL(url string) (releaseSource, bool) {
	rest, found := strings.CutPrefix(url, releasePrefix)
	if !found {
		return releaseSource{}, false
	}

	repo, rest, found := strings.Cut(rest, releaseMiddle)
	if !found {
		return releaseSource{}, false
	}

	tag, _, found := strings.Cut(rest, "/")
	if !found {
		return releaseSource{}, false
	}

	return releaseSource{repo: repo, tag: tag}, true
}

// parseVersionFile reads a version file back into the utilities it names.
// Line order is presentation only, so the check compares the pairs.
func parseVersionFile(content []byte) map[string]string {
	listed := map[string]string{}

	for line := range strings.SplitSeq(string(content), "\n") {
		if match := utilityLine.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			listed[match[1]] = match[2]
		}
	}

	return listed
}

// utilitiesDiffer says how a version file disagrees with what the release
// bundles, or nothing when the two list the same versions.
func utilitiesDiffer(listed, bundled map[string]string) string {
	var wrong []string

	for name, version := range bundled {
		switch found, named := listed[name]; {
		case !named:
			wrong = append(wrong, "leaves "+name+" out")
		case found != version:
			wrong = append(wrong, fmt.Sprintf("says %s %s, but the release bundles %s",
				name, found, version))
		}
	}

	for name := range listed {
		if _, bundles := bundled[name]; !bundles {
			wrong = append(wrong, "names "+name+", which the release does not bundle")
		}
	}

	slices.Sort(wrong)

	return strings.Join(wrong, "; ")
}
