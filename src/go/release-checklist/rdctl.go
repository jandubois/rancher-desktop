// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// rdctlReferencePage documents every rdctl command, from rdctl's own help and
// output.
const rdctlReferencePage = "docs/references/rdctl-command-reference.md"

// rdctlReferenceScript regenerates that page from the rdctl on PATH. It lives
// in the documentation repository, which is where the version rewrite and the
// configuration path it normalizes belong.
const rdctlReferenceScript = "scripts/update-rdctl-reference"

// exampleSnapshot is the snapshot the page's example creates and deletes.
// Regenerating the page runs that example, so the machine gains and loses a
// snapshot of this name.
const exampleSnapshot = "example_snapshot"

// exampleExtension is the extension the page's examples install, list and
// uninstall, so regenerating the page installs and removes it on this machine.
const exampleExtension = "docker/logs-explorer-extension"

// noExtensionsInstalled is what rdctl extension ls prints on a machine with
// no extensions.
const noExtensionsInstalled = "No extensions are installed."

// regenerationEffects is what regenerating the page does to this machine.
const regenerationEffects = "Regenerating the page installs and removes " + exampleExtension +
	", and creates and deletes the snapshot " + exampleSnapshot +
	". Creating the snapshot stops the VM and starts it again."

// reportedVersion matches the line the page shows rdctl reporting itself on.
// The script writes it from the version it is given, so it is the one part of
// the page that names the release.
var reportedVersion = regexp.MustCompile(`rdctl client version: (v\S+?), targeting server version:`)

// listSettingsCommand opens the page's block of rdctl list-settings output.
// The page runs that command twice, once for its help, so the block is the one
// whose command line carries no argument.
const listSettingsCommand = "$ rdctl list-settings"

// settingLine matches one field of the pretty-printed JSON that
// rdctl list-settings prints: its indentation, its name, and its value with
// whatever comma follows.
var settingLine = regexp.MustCompile(`^(\s*)"([^"]+)": (.*)$`)

// hostSettings are the fields of that output which report the machine the page
// was generated on rather than what the documentation describes. The page
// keeps the value it already shows for each of them, so whoever regenerates it
// does not have to put their own configuration back by hand.
//
// kubernetes.version is deliberately not among them. It moves with the
// release, and freezing it would leave the page naming an old default for
// good.
var hostSettings = []string{
	"application.updater.enabled",
	"containerEngine.name",
	"virtualMachine.memoryInGB",
}

// docsReference is step 7b. The page is generated from the build it documents,
// so it can only be written on a machine running that build, and the settings
// it captures are that machine's own.
var docsReference = &Step{
	ID:    "7b",
	Title: "Docs: rdctl reference",
	Kinds: []Kind{Minor},
	Needs: []*Resource{githubDocsRepo},
	Doc: Doc{
		Applies: minorDocs,
		Check: "The release branch of your fork of {docsRepo}, or {docsRepo}'s own main " +
			"branch once the documentation is merged, has " +
			"references/rdctl-command-reference.md reporting {version}, and you have " +
			"marked the page done. Editing it afterwards puts the step back to " +
			"available.",
		Precondition: "gh can push to your fork of {docsRepo} (to {docsRepo} itself " +
			"when you have none), the bundled utilities step is done, " +
			"Rancher Desktop is running, and this machine holds no snapshots and no extensions.",
		Instructions: "Regenerate the rdctl command reference for {version}:\n\n" +
			"1. Start Rancher Desktop, so the commands the page runs are answered by " +
			"the build it documents.\n" +
			"2. In a worktree of your documentation clone on release-{line}, run " +
			"scripts/update-rdctl-reference {version}.\n" +
			"3. Put back the settings that report your own machine: " +
			nameList(hostSettings) + ". Leave the kubernetes version alone, because " +
			"it moves with the release.\n" +
			"4. Read the diff for anything else of yours that reached the page, then " +
			"commit it with a sign-off and push it to release-{line} of your fork of " +
			"{docsRepo}.\n\n" +
			"The script runs every command the page shows, so it creates and deletes a " +
			"snapshot called " + exampleSnapshot + " and installs and removes " +
			exampleExtension + ". Creating the snapshot stops the VM and starts it " +
			"again. The page lists no snapshots and no other extension, and any this " +
			"machine holds would appear in it.\n\n" +
			"Press `m` once the page is the one to ship.",
	},
	Check:        checkDocsReference,
	Precondition: docsReferenceReady,
	Confirms:     referenceInDocs,
	Action: &Action{
		Title:   "Push the rdctl command reference for {version} to {branch} of your documentation fork",
		Writes:  []*Resource{githubDocsFork},
		Summary: referenceSummary,
		Plan:    planDocsReference,
	},
}

// checkDocsReference reports whether the documentation's command reference is
// the one this release ships.
func checkDocsReference(ctx context.Context, run *Run) (Answer, error) {
	tag := run.Release.Version.Tag()

	docs, ref, err := docsLocation(ctx, run)
	if err != nil {
		return Answer{}, err
	}

	where := fmt.Sprintf("%s at %s", docs.repo, ref)

	page, err := docs.FileAtRef(ctx, ref, rdctlReferencePage)
	if errors.Is(err, errNotFound) {
		return Answer{Detail: where + " has no " + rdctlReferencePage}, nil
	}

	if err != nil {
		return Answer{}, err
	}

	match := reportedVersion.FindSubmatch(page)
	if match == nil {
		return Answer{Detail: fmt.Sprintf("%s in %s reports no rdctl version",
			rdctlReferencePage, where)}, nil
	}

	if reported := string(match[1]); reported != tag {
		return Answer{Detail: fmt.Sprintf("%s in %s reports %s",
			rdctlReferencePage, where, reported)}, nil
	}

	return Answer{OK: true, Detail: fmt.Sprintf("%s in %s reports %s",
		rdctlReferencePage, where, tag)}, nil
}

// docsReferenceReady holds the step until the documentation branch carries the
// bundled utilities, and until this machine can generate a page that describes
// the release rather than itself.
func docsReferenceReady(ctx context.Context, run *Run) (Answer, error) {
	if waiting := waitFor(ctx, run, docsUtilities); !waiting.OK {
		return waiting, nil
	}

	if !run.Tools.installed("rdctl") {
		return Answer{Detail: "rdctl is not on PATH; Rancher Desktop installs it"}, nil
	}

	if _, err := run.Tools.run(ctx, "rdctl", "list-settings"); err != nil {
		return Answer{Detail: "Rancher Desktop is not answering rdctl; the page is " +
			"generated from the running build"}, nil
	}

	snapshots, err := run.Tools.run(ctx, "rdctl", "snapshot", "list", "--json")
	if err != nil {
		return Answer{}, fmt.Errorf("reading this machine's snapshots: %w", err)
	}

	if len(bytes.TrimSpace(snapshots)) > 0 {
		return Answer{Detail: "this machine holds snapshots, and the page would list " +
			"them with their timestamps"}, nil
	}

	extensions, err := run.Tools.run(ctx, "rdctl", "extension", "ls")
	if err != nil {
		return Answer{}, fmt.Errorf("reading this machine's extensions: %w", err)
	}

	if string(bytes.TrimSpace(extensions)) != noExtensionsInstalled {
		return Answer{Detail: "this machine has extensions installed, and the page " +
			"would list them beside its own example"}, nil
	}

	return Answer{OK: true}, nil
}

// referenceInDocs is the page a person marks done. A change to it, whether
// theirs or a later regeneration's, puts the step back to available.
func referenceInDocs(ctx context.Context, run *Run) (string, error) {
	docs, ref, err := docsLocation(ctx, run)
	if err != nil {
		return "", err
	}

	page, err := docs.FileAtRef(ctx, ref, rdctlReferencePage)
	if errors.Is(err, errNotFound) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return string(page), nil
}

// referenceSummary names the build the page will be generated from, and what
// generating it does to this machine. The script writes the release's version
// into the page whatever answers, so a release candidate produces a page that
// names the release and shows that candidate's output.
func referenceSummary(ctx context.Context, run *Run) (string, error) {
	tag := run.Release.Version.Tag()

	output, err := run.Tools.run(ctx, "rdctl", "version")
	if err != nil {
		return "", fmt.Errorf("reading the version of the rdctl on PATH: %w", err)
	}

	match := reportedVersion.FindSubmatch(output)
	if match == nil {
		return "", errors.New("rdctl version reports no client version")
	}

	build := string(match[1])

	summary := "The rdctl on PATH is " + build + "."
	if build != tag {
		summary = fmt.Sprintf("The rdctl on PATH is %s, not %s. The page will report %s, "+
			"because the script is given the version, but every command's output comes "+
			"from %s.", build, tag, tag, build)
	}

	return summary + " " + regenerationEffects, nil
}

// planDocsReference regenerates the command reference in a worktree of the
// documentation clone and pushes the commit to the release branch of the
// user's fork. It shares the worktree and the branch with the bundled
// utilities, and opens no pull request, because one pull request carries all
// three parts of the documentation.
func planDocsReference(ctx context.Context, run *Run) ([]Operation, error) {
	clone, err := docsCloneDir(run)
	if err != nil {
		return nil, err
	}

	dir, err := worktreePath(run, "docs")
	if err != nil {
		return nil, err
	}

	docs, ref, err := docsLocation(ctx, run)
	if err != nil {
		return nil, err
	}

	head, err := docs.BranchHead(ctx, ref)
	if err != nil {
		return nil, err
	}

	fork, err := docsFork(ctx, run)
	if err != nil {
		return nil, err
	}

	version := run.Release.Version
	commit := command(dir, "git", "commit", "--signoff", "--message", referenceCommitMessage(version), "--", rdctlReferencePage)

	return []Operation{
		command(clone, "git", "fetch", docs.url, ref),
		{
			Description: fmt.Sprintf("check %.7s of %s out in %s", head, ref, dir),
			Do: func(ctx context.Context, run *Run) error {
				return worktreeAt(ctx, run, clone, dir, head)
			},
		},
		command(dir, filepath.Join(dir, rdctlReferenceScript), version.String()),
		{
			Description: fmt.Sprintf("restore %s in %s, which report this machine",
				nameList(hostSettings), rdctlReferencePage),
			Do: func(ctx context.Context, run *Run) error {
				return normalizeReference(ctx, run, dir, head)
			},
		},
		command(dir, "git", "add", "--", rdctlReferencePage),
		{
			Description: commit.Description + ", if the page changed",
			Do: func(ctx context.Context, run *Run) error {
				return commitIfChanged(ctx, run, &commit)
			},
		},
		command(dir, "git", "push", fork.pushURL, "HEAD:refs/heads/"+run.Release.Branch()),
	}, nil
}

// commitIfChanged commits the page when it differs from the page in the
// checked-out commit. The step stays available until somebody marks the page,
// so the action can run again after its push and regenerate the page it pushed.
func commitIfChanged(ctx context.Context, run *Run, commit *Operation) error {
	staged, err := run.Tools.runIn(ctx, commit.Dir, "git", "diff", "--cached", "--name-only", "--", rdctlReferencePage)
	if err != nil {
		return err
	}

	if strings.TrimSpace(string(staged)) == "" {
		return nil
	}

	_, err = run.Tools.runIn(ctx, commit.Dir, commit.Command, commit.Args...)

	return err
}

// referenceCommitMessage is the subject the documentation's history uses for
// this change.
func referenceCommitMessage(version Version) string {
	return "Update rdctl reference for " + version.String()
}

// normalizeReference puts the host-specific settings of the committed page
// back into the one the script has just written.
func normalizeReference(ctx context.Context, run *Run, dir, head string) error {
	path := filepath.Join(dir, rdctlReferencePage)

	was, err := run.Tools.runIn(ctx, dir, "git", "show", head+":"+rdctlReferencePage)
	if err != nil {
		return fmt.Errorf("reading %s at %.7s: %w", rdctlReferencePage, head, err)
	}

	now, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	page, err := restoreHostSettings(was, now)
	if err != nil {
		return err
	}

	return os.WriteFile(path, page, 0o644)
}

// restoreHostSettings gives the settings a fresh page reports for the
// host-specific fields back to the values the page being replaced shows. Every
// other field passes through, so a setting added since the last release
// reaches the documentation.
func restoreHostSettings(was, now []byte) ([]byte, error) {
	documented := strings.Split(string(was), "\n")
	generated := strings.Split(string(now), "\n")

	from, err := settingsBlock(documented)
	if err != nil {
		return nil, fmt.Errorf("in the page being replaced: %w", err)
	}

	into, err := settingsBlock(generated)
	if err != nil {
		return nil, err
	}

	documentedAt := settingPaths(documented[from.start:from.end])
	generatedAt := settingPaths(generated[into.start:into.end])

	for _, field := range hostSettings {
		at, reported := generatedAt[field]
		source, shown := documentedAt[field]

		switch {
		case !reported && shown:
			return nil, fmt.Errorf("the regenerated %s shows no %s, though the page it replaces does; "+
				"read what the script wrote under %s", rdctlReferencePage, field, listSettingsCommand)
		case !reported:
			continue
		case !shown:
			return nil, fmt.Errorf("%s shows no %s, so the page would report this machine's own",
				rdctlReferencePage, field)
		}

		line, err := restoreValue(generated[into.start+at], documented[from.start+source])
		if err != nil {
			return nil, err
		}

		generated[into.start+at] = line
	}

	return []byte(strings.Join(generated, "\n")), nil
}

// extent is a range of lines, from the first up to but not including the last.
type extent struct {
	start, end int
}

// settingsBlock is where the page shows what rdctl list-settings prints, from
// the line after the command to the fence that closes the block.
func settingsBlock(lines []string) (extent, error) {
	for i, line := range lines {
		if strings.TrimSpace(line) != listSettingsCommand {
			continue
		}

		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "```") {
				return extent{start: i + 1, end: j}, nil
			}
		}

		return extent{}, fmt.Errorf("the %s block in %s is never closed",
			listSettingsCommand, rdctlReferencePage)
	}

	return extent{}, fmt.Errorf("%s shows no %s output", rdctlReferencePage, listSettingsCommand)
}

// settingPaths maps the dotted name of every setting in pretty-printed JSON to
// the line it is on. A line that opens an object or a list holds no value of
// its own, so only the settings themselves are in the result.
func settingPaths(lines []string) map[string]int {
	at := map[string]int{}

	var path []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]") {
			if len(path) > 0 {
				path = path[:len(path)-1]
			}

			continue
		}

		match := settingLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		key, value := match[2], match[3]
		if value == "{" || value == "[" {
			path = append(path, key)

			continue
		}

		at[strings.Join(append(path, key), ".")] = i
	}

	return at
}

// restoreValue writes the value a documented line shows into the line the
// script generated, keeping the generated line's indentation and the comma
// that follows it. The two lines can sit at different places in the object, so
// only the value crosses over.
func restoreValue(generated, documented string) (string, error) {
	shown := settingLine.FindStringSubmatch(documented)
	written := settingLine.FindStringSubmatch(generated)

	if shown == nil || written == nil {
		return "", fmt.Errorf("%q and %q are not both settings", documented, generated)
	}

	value := strings.TrimSuffix(shown[3], ",")
	if strings.HasSuffix(written[3], ",") {
		value += ","
	}

	return fmt.Sprintf("%s%q: %s", written[1], written[2], value), nil
}
