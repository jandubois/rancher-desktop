// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
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

// docsVersionWindow is how many releases the reference page lists. Its table
// shows every utility of every release it names, so the page stays readable
// only while the window is small.
const docsVersionWindow = 3

// utilityLine is how a version file names one utility. The site renders the
// file inside a table cell, so every line ends with a break.
var utilityLine = regexp.MustCompile(`^(\S+): (\S+) <br/>$`)

// versionImport is how the reference page imports one release's version file.
var versionImport = regexp.MustCompile(`^import Version\w+ from '` +
	regexp.QuoteMeta(docsVersionImportDir) + `/v\S+\.md';$`)

// versionRow is how the page's table shows one release: its version, and the
// component the import gives its version file.
var versionRow = regexp.MustCompile(`^\| +v\S+ +\| +<Version\w+ */> +\|$`)

// tableRule is the rule under the table's header. It sets the width of every
// cell in the column below it, so a row written by hand and one written here
// line up.
var tableRule = regexp.MustCompile(`^\|(-+)\|(-+)\|$`)

// versionFileName names the file listing what a release bundles.
func versionFileName(version Version) string { return "v" + version.String() + ".md" }

// versionFilePath is that file's path in the documentation repository.
func versionFilePath(version Version) string {
	return docsVersionDir + "/" + versionFileName(version)
}

// docsVersionImportDir is the version directory as the reference page reaches
// it, one level up from the page's own directory.
var docsVersionImportDir = "../" + path.Base(docsVersionDir)

// versionImportPath is a version file as the reference page imports it.
func versionImportPath(version Version) string {
	return docsVersionImportDir + "/" + versionFileName(version)
}

// importName is the name the page imports a release's version file under:
// Version, then the line with its dot taken out, as the published page has it.
func importName(version Version) string {
	return fmt.Sprintf("Version%d%d", version.Major, version.Minor)
}

// importLine is the line that imports a release's version file into the
// reference page, as the action writes it.
func importLine(version Version) string {
	return fmt.Sprintf("import %s from '%s';", importName(version), versionImportPath(version))
}

// releaseRow matches the table row that shows a release, however it is
// padded.
func releaseRow(version Version) *regexp.Regexp {
	return regexp.MustCompile(`^\| +` + regexp.QuoteMeta(version.Tag()) +
		` +\| +<` + importName(version) + ` */> +\|$`)
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
			"imports it and shows it in its table, and the file lists the " +
			"versions {version} bundles.",
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
	Action: &Action{
		Title: "Push the bundled utility versions for {version} to {branch} of your documentation fork",
		Plan:  planDocsUtilities,
	},
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

	lines := strings.Split(string(page), "\n")

	if !slices.Contains(lines, importLine(version)) {
		return Answer{Detail: fmt.Sprintf("%s in %s does not import %s",
			docsReferencePage, where, name)}, nil
	}

	if !slices.ContainsFunc(lines, releaseRow(version).MatchString) {
		return Answer{Detail: fmt.Sprintf("%s in %s has no row for %s",
			docsReferencePage, where, version.Tag())}, nil
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
	docs, err := run.Docs(ctx)
	if err != nil {
		return nil, "", err
	}

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

// docsFork is the user's fork of the documentation repository, or the
// documentation repository itself when they have no fork of it.
func docsFork(ctx context.Context, run *Run) (*repository, error) {
	docs, err := run.Docs(ctx)
	if err != nil {
		return nil, err
	}

	return docs.Fork(ctx)
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

// docsCommitMessage is the subject the documentation's own history gives these
// commits.
func docsCommitMessage(version Version) string {
	return "Update bundled utilities for " + version.String()
}

// docsChange is what the action writes into the documentation: where the
// branch is cut from, the releases the reference page ends up listing, and the
// version files the window leaves behind.
type docsChange struct {
	// repo and ref are where the release's documentation is now, and head is
	// the commit the worktree checks out.
	repo *repository
	ref  string
	head string
	// window is the releases the page lists once this one is in, newest first.
	window []Version
	// dropped are the version files the window leaves behind, oldest first.
	dropped []Version
	// bundled is what the release bundles, by the name the page calls it.
	bundled map[string]string
}

// docsChangeFor works out what the documentation has to say about this release.
// It changes nothing, so the confirmation can name every file the action
// writes, drops and rewrites before any of it happens.
func docsChangeFor(ctx context.Context, run *Run) (*docsChange, error) {
	docs, ref, err := docsLocation(ctx, run)
	if err != nil {
		return nil, err
	}

	head, err := docs.BranchHead(ctx, ref)
	if err != nil {
		return nil, err
	}

	names, err := docs.FilesAtRef(ctx, ref, docsVersionDir)
	if err != nil {
		return nil, err
	}

	bundled, err := bundledVersions(ctx, run)
	if err != nil {
		return nil, err
	}

	listed := listedVersions(names)
	window := versionWindow(listed, run.Release.Version)

	return &docsChange{
		repo:    docs,
		ref:     ref,
		head:    head,
		window:  window,
		dropped: droppedVersions(listed, window),
		bundled: bundled,
	}, nil
}

// planDocsUtilities writes the versions this release bundles into a worktree of
// the documentation clone and pushes the commit to the release branch of the
// user's fork. It opens no pull request, because the rdctl reference and the
// versioning snapshot go on the same branch and one pull request carries all
// three.
func planDocsUtilities(ctx context.Context, run *Run) ([]Operation, error) {
	clone, err := docsCloneDir(run)
	if err != nil {
		return nil, err
	}

	dir, err := worktreePath(run, "docs")
	if err != nil {
		return nil, err
	}

	change, err := docsChangeFor(ctx, run)
	if err != nil {
		return nil, err
	}

	fork, err := docsFork(ctx, run)
	if err != nil {
		return nil, err
	}

	version := run.Release.Version

	operations := []Operation{
		command(clone, "git", "fetch", change.repo.url, change.ref),
		{
			Description: fmt.Sprintf("check %.7s of %s out in %s", change.head, change.ref, dir),
			Do: func(ctx context.Context, run *Run) error {
				return worktreeAt(ctx, run, clone, dir, change.head)
			},
		},
		{
			Description: fmt.Sprintf("write %s, listing the %d utilities %s bundles",
				versionFilePath(version), len(change.bundled), version),
			Do: func(context.Context, *Run) error {
				return os.WriteFile(filepath.Join(dir, versionFilePath(version)),
					versionFileContent(change.bundled), 0o644)
			},
		},
	}

	for _, dropped := range change.dropped {
		operations = append(operations, Operation{
			Description: fmt.Sprintf("drop %s, so the page lists %d releases",
				versionFilePath(dropped), docsVersionWindow),
			Do: func(context.Context, *Run) error {
				return os.Remove(filepath.Join(dir, versionFilePath(dropped)))
			},
		})
	}

	return append(operations,
		Operation{
			Description: fmt.Sprintf("list %s in %s",
				nameList(versionTags(change.window)), docsReferencePage),
			Do: func(context.Context, *Run) error {
				return writeReferencePage(dir, change.window)
			},
		},
		command(dir, "git", "add", "--all", "--", docsVersionDir, docsReferencePage),
		command(dir, "git", "commit", "--signoff", "--message", docsCommitMessage(version), "--", docsVersionDir, docsReferencePage),
		command(dir, "git", "push", fork.pushURL, "HEAD:refs/heads/"+run.Release.Branch()),
	), nil
}

// versionTags names releases the way the reference page's table does.
func versionTags(versions []Version) []string {
	named := make([]string, 0, len(versions))
	for _, version := range versions {
		named = append(named, version.Tag())
	}

	return named
}

// versionFileContent is the version file for what a release bundles, one
// utility per line, sorted by name. The published files are in no order a sort
// produces and the check compares the pairs, so sorting costs nothing and
// makes a diff between two releases readable.
func versionFileContent(bundled map[string]string) []byte {
	var out strings.Builder

	for _, name := range slices.Sorted(maps.Keys(bundled)) {
		fmt.Fprintf(&out, "%s: %s <br/>\n", name, bundled[name])
	}

	return []byte(out.String())
}

// listedVersions are the releases a directory of version files names.
func listedVersions(names []string) []Version {
	versions := make([]Version, 0, len(names))

	for _, name := range names {
		base, isMarkdown := strings.CutSuffix(name, ".md")
		if !isMarkdown {
			continue
		}

		if version, err := ParseVersion(base); err == nil {
			versions = append(versions, version)
		}
	}

	return versions
}

// versionWindow is the releases the reference page lists once this one is
// added, newest first.
func versionWindow(listed []Version, release Version) []Version {
	window := slices.Clone(listed)

	if !slices.Contains(window, release) {
		window = append(window, release)
	}

	slices.SortFunc(window, func(a, b Version) int { return CompareVersions(b, a) })

	return window[:min(len(window), docsVersionWindow)]
}

// droppedVersions are the version files the window leaves behind, oldest first.
func droppedVersions(listed, window []Version) []Version {
	var dropped []Version

	for _, version := range listed {
		if !slices.Contains(window, version) {
			dropped = append(dropped, version)
		}
	}

	slices.SortFunc(dropped, CompareVersions)

	return dropped
}

// writeReferencePage rewrites the reference page in a worktree, so it lists
// the window.
func writeReferencePage(dir string, window []Version) error {
	file := filepath.Join(dir, docsReferencePage)

	page, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	updated, err := rewriteReferencePage(page, window)
	if err != nil {
		return err
	}

	return os.WriteFile(file, updated, 0o644)
}

// rewriteReferencePage puts the window into the reference page: an import per
// release, oldest first as the page has them, and a row per release, newest
// first as the table shows them. It replaces those two runs of lines and
// leaves the rest of the page alone, so the prose and the formatting stay as
// whoever wrote them had them.
func rewriteReferencePage(page []byte, window []Version) ([]byte, error) {
	lines := strings.Split(string(page), "\n")

	versionWidth, importWidth, err := tableWidths(lines)
	if err != nil {
		return nil, err
	}

	imports := make([]string, 0, len(window))
	for _, version := range slices.SortedFunc(slices.Values(window), CompareVersions) {
		imports = append(imports, importLine(version))
	}

	rows := make([]string, 0, len(window))
	for _, version := range window {
		rows = append(rows, referenceRow(version, versionWidth, importWidth))
	}

	lines, err = replaceMatches(lines, versionImport, imports, "imports of the version files")
	if err != nil {
		return nil, err
	}

	lines, err = replaceMatches(lines, versionRow, rows, "table rows for the releases")
	if err != nil {
		return nil, err
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// tableWidths are the widths the rule sets for the table's two columns.
func tableWidths(lines []string) (int, int, error) {
	for _, line := range lines {
		if match := tableRule.FindStringSubmatch(line); match != nil {
			return len(match[1]), len(match[2]), nil
		}
	}

	return 0, 0, fmt.Errorf("%s has no table to write the releases into", docsReferencePage)
}

// referenceRow is a release's row of the table, padded to the widths the rule
// sets.
func referenceRow(version Version, versionWidth, importWidth int) string {
	return fmt.Sprintf("| %-*s| %-*s|",
		versionWidth-1, version.Tag(), importWidth-1, "<"+importName(version)+" />")
}

// replaceMatches puts the lines given where the first line a pattern matches
// is and drops the rest it matches, so a rewrite touches nothing else in the
// file. Dropping them all is what a page whose imports somebody has written
// something between needs: leaving one behind would import a file the action
// has deleted.
func replaceMatches(lines []string, pattern *regexp.Regexp, with []string, what string) ([]string, error) {
	kept := make([]string, 0, len(lines)+len(with))
	written := false

	for _, line := range lines {
		switch {
		case !pattern.MatchString(line):
			kept = append(kept, line)
		case !written:
			kept = append(kept, with...)
			written = true
		}
	}

	if !written {
		return nil, fmt.Errorf("%s has no %s to rewrite", docsReferencePage, what)
	}

	return kept, nil
}
