// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	testDocsRepo = "rancher/docs.rancherdesktop.io"
	testLogin    = "me"
	testDocsFork = testLogin + "/docs.rancherdesktop.io"
	testDocsFile = docsVersionDir + "/v1.25.0.md"
)

// docsDependencies is dependencies.yaml cut to what this step reads: three
// bundled utilities, and the two guest images with the release URLs their
// nerdctl pins are read from.
const docsDependencies = `WSLDistro:
  assets:
    - platform: wsl
      url: https://github.com/rancher-sandbox/rancher-desktop-wsl-distro/releases/download/v0.100/distro-0.100.tar
  version: "0.100"
alpineLimaISO:
  assets:
    - arch: amd64
      platform: linux
      url: https://github.com/rancher-sandbox/alpine-lima/releases/download/v0.2.47.rd9/alpine-lima-rd-3.24.1-x86_64.iso
  version:
    alpineVersion: 3.24.1
    isoVersion: 0.2.47.rd9
dockerCLI:
  version: 29.8.1
helm:
  version: 4.3.0
trivy:
  version: 0.74.0
`

// limaMakefile and wslVersions are the files each guest image pins its
// nerdctl in. The Lima image writes a bare version and the WSL distribution
// writes a tag.
const (
	limaMakefile = "ARCH?=x86_64\nNERDCTL_VERSION=2.2.2\nALPINE_VERSION=3.24.1\n"
	wslVersions  = "NERDCTL_REPO=containerd/nerdctl\nNERDCTL_VERSION=v2.2.2\nOPENRESTY_VERSION=v0.0.7\n"
)

// docsVersionFileText is the version file a release manager writes. It holds
// one utility per line, sorted by name, each ending in the break the site's
// table cell needs.
const docsVersionFileText = "docker: 29.8.1 <br/>\nhelm: 4.3.0 <br/>\nnerdctl: 2.2.2 <br/>\ntrivy: 0.74.0 <br/>\n"

// docsReferenceText is the page that imports the version files, cut to the
// import the check reads.
const docsReferenceText = "---\ntitle: Bundled Utilities\n---\n\n" +
	"import Version124 from '../bundled-utilities-version-info/v1.24.0.md';\n" +
	"import Version125 from '../bundled-utilities-version-info/v1.25.0.md';\n"

// fileQuery is the command that reads a file from a repository at a ref.
func fileQuery(repo, ref, path string) string {
	return "gh api repos/" + repo + "/contents/" + path + "?ref=" + ref +
		" --header Accept: application/vnd.github.raw"
}

// headQuery is the command that reads a branch's head commit.
func headQuery(repo, branch string) string {
	return "gh api repos/" + repo + "/branches/" + branch + " --jq .commit.sha"
}

// docsAnswers is every command the check runs when the documentation is on
// the fork's release branch and lists what the release bundles.
func docsAnswers() map[string]string {
	return map[string]string{
		"gh api repos/" + testDocsRepo + " --jq .permissions.push":                        "true\n",
		"gh api user --jq .login":                                                         testLogin + "\n",
		"gh api repos/" + testDocsFork + " --jq .parent.full_name":                        testDocsRepo + "\n",
		headQuery(testDocsFork, testBranch):                                               testHead + "\n",
		fileQuery(testDocsFork, testBranch, testDocsFile):                                 docsVersionFileText,
		fileQuery(testDocsFork, testBranch, docsReferencePage):                            docsReferenceText,
		fileQuery(testRepo, testBranch, dependenciesFile):                                 docsDependencies,
		fileQuery("rancher-sandbox/alpine-lima", "v0.2.47.rd9", "Makefile"):               limaMakefile,
		fileQuery("rancher-sandbox/rancher-desktop-wsl-distro", "v0.100", "versions.env"): wslVersions,
	}
}

// docsRun is a refresh whose profile names a documentation repository.
func docsRun(t *testing.T, tools *fakeTools) *Run {
	t.Helper()

	run := checklistRun(t, testRelease, map[Line]string{testRelease.Line(): testHead}, tools)
	run.Profile.GitHub.DocsRepo = testDocsRepo

	return run
}

func TestDocsUtilitiesIsDoneWhenTheDocsListWhatTheReleaseBundles(t *testing.T) {
	run := docsRun(t, &fakeTools{output: docsAnswers()})

	if status := run.Status(context.Background(), docsUtilities); status.State != Done {
		t.Errorf("the docs step was %s: %s", status.State, status.Detail)
	}
}

func TestDocsUtilitiesReadsMainOnceTheReleaseBranchIsGone(t *testing.T) {
	answers := docsAnswers()
	delete(answers, headQuery(testDocsFork, testBranch))
	delete(answers, fileQuery(testDocsFork, testBranch, testDocsFile))
	delete(answers, fileQuery(testDocsFork, testBranch, docsReferencePage))

	answers[fileQuery(testDocsRepo, defaultBranch, testDocsFile)] = docsVersionFileText
	answers[fileQuery(testDocsRepo, defaultBranch, docsReferencePage)] = docsReferenceText

	tools := &fakeTools{
		output: answers,
		stderr: map[string]string{
			// What GitHub answers for a deleted release branch.
			headQuery(testDocsFork, testBranch): "gh: Branch not found (HTTP 404)",
		},
	}

	if status := docsRun(t, tools).Status(context.Background(), docsUtilities); status.State != Done {
		t.Errorf("the docs step was %s: %s", status.State, status.Detail)
	}
}

func TestDocsUtilitiesIsAvailableWhenAVersionIsStale(t *testing.T) {
	answers := docsAnswers()
	answers[fileQuery(testDocsFork, testBranch, testDocsFile)] =
		strings.Replace(docsVersionFileText, "helm: 4.3.0", "helm: 4.2.3", 1)

	status := docsRun(t, &fakeTools{output: answers}).Status(context.Background(), docsUtilities)

	if status.State != Available {
		t.Fatalf("a stale version was %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "says helm 4.2.3, but the release bundles 4.3.0") {
		t.Errorf("the line did not name the stale version: %s", status.Detail)
	}
}

func TestDocsUtilitiesIsAvailableWhenTheReferencePageDoesNotImportTheFile(t *testing.T) {
	answers := docsAnswers()
	answers[fileQuery(testDocsFork, testBranch, docsReferencePage)] =
		strings.Replace(docsReferenceText, "v1.25.0.md", "v1.23.0.md", 1)

	status := docsRun(t, &fakeTools{output: answers}).Status(context.Background(), docsUtilities)

	if status.State != Available || !strings.Contains(status.Detail, "does not import") {
		t.Errorf("an unimported file was %s: %s", status.State, status.Detail)
	}
}

func TestDocsUtilitiesIsAvailableWithNoVersionFile(t *testing.T) {
	answers := docsAnswers()
	delete(answers, fileQuery(testDocsFork, testBranch, testDocsFile))

	tools := &fakeTools{
		output: answers,
		stderr: map[string]string{
			fileQuery(testDocsFork, testBranch, testDocsFile): "gh: Not Found (HTTP 404)",
		},
	}

	status := docsRun(t, tools).Status(context.Background(), docsUtilities)

	if status.State != Available || !strings.Contains(status.Detail, "has no v1.25.0.md") {
		t.Errorf("a missing file was %s: %s", status.State, status.Detail)
	}
}

func TestNerdctlComesFromTheGuestImagePins(t *testing.T) {
	run := docsRun(t, &fakeTools{output: docsAnswers()})

	version, err := nerdctlVersion(context.Background(), run, []byte(docsDependencies))
	if err != nil {
		t.Fatal(err)
	}

	if version != "2.2.2" {
		t.Errorf("the pins gave nerdctl %q", version)
	}
}

func TestNerdctlRefusesWhenTheGuestImagesDisagree(t *testing.T) {
	answers := docsAnswers()
	answers[fileQuery("rancher-sandbox/rancher-desktop-wsl-distro", "v0.100", "versions.env")] =
		strings.Replace(wslVersions, "v2.2.2", "v2.2.1", 1)

	run := docsRun(t, &fakeTools{output: answers})

	_, err := nerdctlVersion(context.Background(), run, []byte(docsDependencies))
	if err == nil || !strings.Contains(err.Error(), "2.2.1 in the WSL distribution") {
		t.Errorf("images shipping different versions gave %v", err)
	}
}

func TestReleaseURLNamesTheRepositoryAndTag(t *testing.T) {
	source, ok := parseReleaseURL(
		"https://github.com/rancher-sandbox/alpine-lima/releases/download/v0.2.47.rd9/alpine-lima-rd-3.24.1-x86_64.iso")
	if !ok || source.repo != "rancher-sandbox/alpine-lima" || source.tag != "v0.2.47.rd9" {
		t.Errorf("the ISO URL gave %+v, %v", source, ok)
	}

	// Several dependencies are hosted outside GitHub, and those name no
	// repository to read a pin from. Only the host can decide that, because
	// any site can use the same path.
	for _, url := range []string{
		"https://get.helm.sh/helm-v4.3.0-darwin-arm64.tar.gz",
		"https://charts.example.invalid/releases/download/v1.0.0/chart.tgz",
	} {
		if source, ok := parseReleaseURL(url); ok {
			t.Errorf("%s was read as the release %+v", url, source)
		}
	}
}

// Every published version file lists spin, spin-shim and spin-operator, an
// order no sort produces, so the check compares the pairs and leaves the
// order to whoever wrote the file.
func TestVersionFileIsReadWhateverOrderItsLinesAreIn(t *testing.T) {
	published := parseVersionFile([]byte(
		"spin: 4.1.0 <br/>\nspin-shim: 0.25.1 <br/>\nspin-operator: 0.6.1 <br/>\n"))
	sorted := parseVersionFile([]byte(
		"spin: 4.1.0 <br/>\nspin-operator: 0.6.1 <br/>\nspin-shim: 0.25.1 <br/>\n"))

	if !maps.Equal(published, sorted) {
		t.Errorf("line order changed what the file says: %v against %v", published, sorted)
	}

	if wrong := utilitiesDiffer(published, sorted); wrong != "" {
		t.Errorf("the same versions in another order read as %s", wrong)
	}
}

// The release branch keeps moving after the release, so once the tag exists
// only the tag still says what shipped.
func TestBundledVersionsComeFromTheTagOnceItIsPushed(t *testing.T) {
	run := docsRun(t, &fakeTools{output: docsAnswers()})

	if ref := bundledRef(run); ref != testBranch {
		t.Errorf("before the tag the versions came from %s", ref)
	}

	run.Refs.Tags[testRelease] = testHead

	if ref := bundledRef(run); ref != run.Release.Tag() {
		t.Errorf("after the tag the versions came from %s", ref)
	}
}

// A file can also miss a utility the release bundles, or keep one it stopped
// bundling, and the line has to name which.
func TestUtilitiesDifferNamesWhatIsMissingAndWhatIsExtra(t *testing.T) {
	listed := map[string]string{"docker": "29.8.1", "kuberlr": "0.8.0"}
	bundled := map[string]string{"docker": "29.8.1", "helm": "4.3.0"}

	wrong := utilitiesDiffer(listed, bundled)
	if wrong != "leaves helm out; names kuberlr, which the release does not bundle" {
		t.Errorf("the difference read as %q", wrong)
	}
}

// docsRemoteOutput is `git remote --verbose` from the documentation clone: the
// fork the branch is pushed to, the repository it is cut from, and a push URL
// somebody set to keep a remote from being pushed to at all.
const docsRemoteOutput = "origin\thttps://github.com/" + testDocsFork + "/ (fetch)\n" +
	"origin\thttps://github.com/" + testDocsFork + "/ (push)\n" +
	"upstream\tgit@github.com:" + testDocsRepo + " (fetch)\n" +
	"upstream\tDISABLED (push)\n"

// testDocsHead is the commit the documentation's main branch points at, which
// is what the worktree checks out.
const testDocsHead = "1234abcd1234abcd1234abcd1234abcd1234abcd"

// listQuery is the command that lists the files in a directory at a ref.
func listQuery(repo, ref, dir string) string {
	return "gh api repos/" + repo + "/contents/" + dir + "?ref=" + ref + " --jq .[].name"
}

// docsPlanRun is a refresh whose settings name a documentation clone, with the
// release branch not on the fork yet, so the work is cut from the documentation
// repository's own main branch.
func docsPlanRun(t *testing.T, clone string) (*Run, *fakeTools) {
	t.Helper()

	inACacheOfItsOwn(t)

	answers := docsAnswers()
	delete(answers, headQuery(testDocsFork, testBranch))

	answers[headQuery(testDocsRepo, defaultBranch)] = testDocsHead + "\n"
	answers[listQuery(testDocsRepo, defaultBranch, docsVersionDir)] = "v1.22.0.md\nv1.23.0.md\nv1.24.0.md\n"
	answers["git remote --verbose"] = docsRemoteOutput

	tools := &fakeTools{
		output: answers,
		stderr: map[string]string{
			headQuery(testDocsFork, testBranch): "gh: Branch not found (HTTP 404)",
		},
	}

	run := docsRun(t, tools)
	run.Settings = Settings{DocsClone: clone}

	return run, tools
}

func TestTheDocsActionCutsFromMainAndPushesToTheFork(t *testing.T) {
	const clone = "/clones/docs"

	run, _ := docsPlanRun(t, clone)

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	dir, err := worktreePath(run, "docs")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"git fetch git@github.com:" + testDocsRepo + " " + defaultBranch,
		"check 1234abc of " + defaultBranch + " out in " + dir,
		"write " + docsVersionDir + "/v1.25.0.md, listing the 4 utilities 1.25.0 bundles",
		"drop " + docsVersionDir + "/v1.22.0.md, so the page lists 3 releases",
		"list v1.25.0, v1.24.0, and v1.23.0 in " + docsReferencePage,
		"git add --all -- " + docsVersionDir + " " + docsReferencePage,
		`git commit --signoff --message "Update bundled utilities for 1.25.0"`,
		"git push https://github.com/" + testDocsFork + "/ HEAD:refs/heads/" + testBranch,
	}

	shown := make([]string, 0, len(operations))
	for _, operation := range operations {
		shown = append(shown, operation.Description)
	}

	if !slices.Equal(shown, want) {
		t.Errorf("the action would run\n%s\n\nwant\n%s",
			strings.Join(shown, "\n"), strings.Join(want, "\n"))
	}

	// The fetch writes to the documentation clone's object store, and every
	// command after the checkout works on the files in the worktree.
	for _, operation := range operations {
		switch {
		case operation.Command == "":
		case operation.Args[0] == "fetch":
			if operation.Dir != clone {
				t.Errorf("the fetch ran in %q, not the documentation clone", operation.Dir)
			}
		case operation.Dir != dir:
			t.Errorf("%s ran in %q, not the worktree", operation.Description, operation.Dir)
		}
	}
}

// The clone the tool was started in has no remote for the documentation
// repository, so reading the remotes there would push to a URL the user has
// never pushed to.
func TestTheDocsRemotesAreReadInTheDocumentationClone(t *testing.T) {
	const clone = "/clones/docs"

	run, tools := docsPlanRun(t, clone)

	if _, err := planDocsUtilities(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	ranOnlyIn(t, tools, "git remote --verbose", clone)
}

func TestTheDocsActionSaysWhereToWriteTheClonePath(t *testing.T) {
	run, _ := docsPlanRun(t, "")

	_, err := planDocsUtilities(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "docsClone") ||
		!strings.Contains(err.Error(), settingsFile) {
		t.Errorf("a profile with no clone path gave %v", err)
	}
}

// The action rewrites the imports and the table rows and nothing else, so
// regenerating a published page gives back what its author wrote.
func TestTheReferencePageIsRewrittenAsPublished(t *testing.T) {
	before := readTestFile(t, "bundled-utilities-before.md")
	after := readTestFile(t, "bundled-utilities-after.md")

	window := []Version{{Major: 1, Minor: 24}, {Major: 1, Minor: 23}, {Major: 1, Minor: 22}}

	rewritten, err := rewriteReferencePage(before, window)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(rewritten, after) {
		t.Errorf("the page was rewritten as\n%s\nwant\n%s", rewritten, after)
	}
}

// A page somebody restructured is one the tool cannot write into, and putting
// the imports or the rows anywhere else would leave the page broken.
func TestRewritingRefusesAPageItCannotWriteInto(t *testing.T) {
	before := readTestFile(t, "bundled-utilities-before.md")

	for what, page := range map[string][]byte{
		"no table":   bytes.ReplaceAll(before, []byte("|"), []byte(":")),
		"no rows":    withoutLines(before, "| v1."),
		"no imports": withoutLines(before, "import Version"),
	} {
		if _, err := rewriteReferencePage(page, []Version{{Major: 1, Minor: 24}}); err == nil {
			t.Errorf("a page with %s was rewritten anyway", what)
		}
	}
}

// withoutLines is a page with every line holding the text taken out.
func withoutLines(page []byte, holding string) []byte {
	var kept []string

	for line := range strings.SplitSeq(string(page), "\n") {
		if !strings.Contains(line, holding) {
			kept = append(kept, line)
		}
	}

	return []byte(strings.Join(kept, "\n"))
}

// readTestFile is a file of captured output kept beside the tests.
func readTestFile(t *testing.T, name string) []byte {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}

	return content
}

// Every published version file lists spin, spin-shim and spin-operator, so
// sorting changes the order but not what the check reads.
func TestTheVersionFileIsOneSortedLinePerUtility(t *testing.T) {
	content := versionFileContent(map[string]string{
		"spin-shim": "0.25.1", "spin": "4.0.2", "spin-operator": "0.6.1",
	})

	want := "spin: 4.0.2 <br/>\nspin-operator: 0.6.1 <br/>\nspin-shim: 0.25.1 <br/>\n"
	if string(content) != want {
		t.Errorf("the file reads\n%s", content)
	}
}

// A minor release joins the window and pushes the oldest file out. A second
// attempt finds itself already listed and drops nothing more.
func TestTheWindowKeepsTheNewestReleasesAndDropsTheRest(t *testing.T) {
	listed := []Version{{Major: 1, Minor: 22}, {Major: 1, Minor: 23}, {Major: 1, Minor: 24}}
	release := Version{Major: 1, Minor: 25}

	window := versionWindow(listed, release)
	if want := []Version{release, {Major: 1, Minor: 24}, {Major: 1, Minor: 23}}; !slices.Equal(window, want) {
		t.Errorf("the page would list %v", window)
	}

	if dropped := droppedVersions(listed, window); !slices.Equal(dropped, []Version{{Major: 1, Minor: 22}}) {
		t.Errorf("the action would drop %v", dropped)
	}

	if again := versionWindow(append(listed, release), release); !slices.Equal(again, window) {
		t.Errorf("a second attempt would list %v", again)
	}
}

// The directory holds the version files and nothing else today, so anything
// that is not one names no release rather than failing the step. A name the
// page cannot import counts for nothing however much it reads like a version.
func TestOnlyVersionFilesCountAsReleases(t *testing.T) {
	listed := listedVersions([]string{"v1.24.0.md", "README.md", "v1.23.0.md", "notes.txt", "v1.22.0"})

	want := []Version{{Major: 1, Minor: 24}, {Major: 1, Minor: 23}}
	if !slices.Equal(listed, want) {
		t.Errorf("the directory read as %v", listed)
	}
}

// Somebody adding an import between the version imports would otherwise leave
// the ones after it behind, importing a file the action has deleted.
func TestRewritingDropsEveryVersionImportItFinds(t *testing.T) {
	split := bytes.Replace(readTestFile(t, "bundled-utilities-before.md"),
		[]byte("import Version122 from"),
		[]byte("import Pricing from './pricing.md';\nimport Version122 from"), 1)

	rewritten, err := rewriteReferencePage(split, []Version{{Major: 1, Minor: 24}})
	if err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{"Version121", "Version122", "Version123"} {
		if bytes.Contains(rewritten, []byte(gone)) {
			t.Errorf("%s is still imported:\n%s", gone, rewritten)
		}
	}

	for _, want := range []string{"Version124", "Pricing"} {
		if !bytes.Contains(rewritten, []byte(want)) {
			t.Errorf("%s is missing:\n%s", want, rewritten)
		}
	}
}

// git adds a worktree to whichever clone the command runs in, so the wrong one
// would check a commit of the release repository out.
func TestTheWorktreeIsAddedInTheDocumentationClone(t *testing.T) {
	const clone = "/clones/docs"

	run, tools := docsPlanRun(t, clone)
	tools.anyCommand = true

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	dir, err := worktreePath(run, "docs")
	if err != nil {
		t.Fatal(err)
	}

	for _, operation := range operations {
		if strings.HasPrefix(operation.Description, "check ") {
			if err := operation.perform(context.Background(), run, io.Discard); err != nil {
				t.Fatal(err)
			}
		}
	}

	ranOnlyIn(t, tools, "git worktree add --detach "+dir+" "+testDocsHead, clone)
}
