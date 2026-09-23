// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// referenceFixture is the published command reference, whole. It reports
// v1.24.0, so against the 1.25.0 release of the other tests it is the page a
// release manager has not regenerated yet.
const referenceFixture = "testdata/rdctl-command-reference.md"

// documentedKubernetes and generatedKubernetes are the Kubernetes version the
// page shows and the one a newer machine reports. The field moves with the
// release, so the generated value is the one that has to survive.
const (
	documentedKubernetes = `"version": "1.36.2"`
	generatedKubernetes  = `"version": "1.36.4"`
)

// hostConfiguration turns the published page into the one the script writes on
// a machine configured differently: the three host-specific settings, and the
// Kubernetes version, which is not one of them. Only the updater's line needs
// its parent to tell it from the other settings that are enabled.
var hostConfiguration = strings.NewReplacer(
	"\"updater\": {\n      \"enabled\": true", "\"updater\": {\n      \"enabled\": false",
	`"name": "moby"`, `"name": "containerd"`,
	`"memoryInGB": 6`, `"memoryInGB": 4`,
	documentedKubernetes, generatedKubernetes,
)

// documentedPage is the published command reference.
func documentedPage(t *testing.T) []byte {
	t.Helper()

	page, err := os.ReadFile(referenceFixture)
	if err != nil {
		t.Fatal(err)
	}

	return page
}

// generatedPage is that page as the script writes it on a machine of its own.
func generatedPage(t *testing.T, documented []byte) []byte {
	t.Helper()

	generated := []byte(hostConfiguration.Replace(string(documented)))
	if string(generated) == string(documented) {
		t.Fatal("the fixture no longer holds the settings these tests change")
	}

	return generated
}

// differingLines are the lines of want that have changed in got, as got shows
// them.
func differingLines(want, got string) []string {
	was, now := strings.Split(want, "\n"), strings.Split(got, "\n")
	if len(was) != len(now) {
		return []string{"the pages have different line counts"}
	}

	var changed []string

	for i := range was {
		if was[i] != now[i] {
			changed = append(changed, now[i])
		}
	}

	return changed
}

func TestRestoreHostSettingsKeepsOnlyTheKubernetesVersion(t *testing.T) {
	documented := documentedPage(t)
	generated := generatedPage(t, documented)

	restored, err := restoreHostSettings(documented, generated)
	if err != nil {
		t.Fatal(err)
	}

	changed := differingLines(string(documented), string(restored))
	want := []string{"    " + generatedKubernetes + ","}

	if !slices.Equal(changed, want) {
		t.Errorf("the restored page differs from the published one on %q, want %q", changed, want)
	}
}

func TestRestoreHostSettingsChangesOnlyTheFieldsItDeclares(t *testing.T) {
	documented := documentedPage(t)
	generated := generatedPage(t, documented)

	restored, err := restoreHostSettings(documented, generated)
	if err != nil {
		t.Fatal(err)
	}

	changed := differingLines(string(generated), string(restored))
	want := []string{
		`      "enabled": true`,
		`    "name": "moby"`,
		`    "memoryInGB": 6,`,
	}

	if !slices.Equal(changed, want) {
		t.Errorf("restoring changed %q, want %q", changed, want)
	}
}

func TestRestoreHostSettingsRefusesAFieldThePageNeverShowed(t *testing.T) {
	documented := documentedPage(t)
	generated := generatedPage(t, documented)
	without := strings.Replace(string(documented), `    "memoryInGB": 6,`+"\n", "", 1)

	_, err := restoreHostSettings([]byte(without), generated)
	if err == nil {
		t.Fatal("restoring accepted a page with no documented memoryInGB")
	}

	if !strings.Contains(err.Error(), "virtualMachine.memoryInGB") {
		t.Errorf("the failure did not name the field: %v", err)
	}
}

func TestSettingsBlockSkipsTheHelpOutput(t *testing.T) {
	lines := strings.Split(string(documentedPage(t)), "\n")

	block, err := settingsBlock(lines)
	if err != nil {
		t.Fatal(err)
	}

	if first := lines[block.start]; first != "{" {
		t.Errorf("the block starts at %q, want the settings object", first)
	}

	if fence := lines[block.end]; !strings.HasPrefix(fence, "```") {
		t.Errorf("the block ends at %q, want the fence that closes it", fence)
	}
}

func TestSettingPathsNamesEveryRepeatedKeyApart(t *testing.T) {
	lines := strings.Split(string(documentedPage(t)), "\n")

	block, err := settingsBlock(lines)
	if err != nil {
		t.Fatal(err)
	}

	at := settingPaths(lines[block.start:block.end])

	for _, path := range []string{
		"application.telemetry.enabled",
		"application.updater.enabled",
		"containerEngine.allowedImages.enabled",
		"kubernetes.enabled",
		"virtualMachine.memoryInGB",
		"containerEngine.allowedImages.patterns",
		// This one comes after the multi-line list, so it is only found
		// under its own name while the list's closing bracket pops it.
		"experimental.virtualMachine.sshPortForwarder",
	} {
		if _, found := at[path]; !found {
			t.Errorf("%s is not among the settings the page shows", path)
		}
	}

	// A list the page writes over several lines opens a path of its own, and
	// its elements have no name to be found under.
	if _, found := at["experimental.virtualMachine.proxy.noproxy"]; found {
		t.Error("a multi-line list was read as a setting")
	}
}

func TestRestoreValueKeepsTheGeneratedComma(t *testing.T) {
	for _, one := range []struct {
		generated, documented, want string
	}{
		{`    "name": "containerd",`, `    "name": "moby"`, `    "name": "moby",`},
		{`    "name": "containerd"`, `    "name": "moby",`, `    "name": "moby"`},
		{`      "memoryInGB": 4,`, `  "memoryInGB": 6,`, `      "memoryInGB": 6,`},
	} {
		got, err := restoreValue(one.generated, one.documented)
		if err != nil {
			t.Fatal(err)
		}

		if got != one.want {
			t.Errorf("restoring %q from %q gave %q, want %q",
				one.generated, one.documented, got, one.want)
		}
	}
}

// referenceAnswers is every command the check runs when the command reference
// is on the fork's release branch.
func referenceAnswers(page string) map[string]string {
	return map[string]string{
		"gh api repos/" + testDocsRepo + " --jq .permissions.push": "true\n",
		"gh api user --jq .login":                                  testLogin + "\n",
		"gh api repos/" + testDocsFork + " --jq .parent.full_name": testDocsRepo + "\n",
		headQuery(testDocsFork, testBranch):                        testHead + "\n",
		fileQuery(testDocsFork, testBranch, rdctlReferencePage):    page,
	}
}

func TestDocsReferenceIsDoneWhenThePageReportsTheRelease(t *testing.T) {
	page := strings.Replace(string(documentedPage(t)), "v1.24.0,", testRelease.Tag()+",", 1)
	run := docsRun(t, &fakeTools{output: referenceAnswers(page)})

	answer, err := checkDocsReference(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if !answer.OK || !strings.Contains(answer.Detail, testRelease.Tag()) {
		t.Errorf("a page reporting the release answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestDocsReferenceIsAvailableWhileThePageReportsAnOlderRelease(t *testing.T) {
	run := docsRun(t, &fakeTools{output: referenceAnswers(string(documentedPage(t)))})

	answer, err := checkDocsReference(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, "reports v1.24.0") {
		t.Errorf("the published page answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestDocsReferenceIsAvailableWithNoPage(t *testing.T) {
	answers := referenceAnswers("")
	delete(answers, fileQuery(testDocsFork, testBranch, rdctlReferencePage))

	tools := &fakeTools{
		output: answers,
		stderr: map[string]string{
			fileQuery(testDocsFork, testBranch, rdctlReferencePage): "gh: Not Found (HTTP 404)",
		},
	}

	answer, err := checkDocsReference(context.Background(), docsRun(t, tools))
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, "has no "+rdctlReferencePage) {
		t.Errorf("a missing page answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestDocsReferenceIsAvailableWhenThePageReportsNoVersion(t *testing.T) {
	page := strings.Replace(string(documentedPage(t)), "rdctl client version: v1.24.0", "", 1)

	answer, err := checkDocsReference(context.Background(),
		docsRun(t, &fakeTools{output: referenceAnswers(page)}))
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, "reports no rdctl version") {
		t.Errorf("a page with no version answered %v: %s", answer.OK, answer.Detail)
	}
}

// snapshotQuery is the command that reads this machine's snapshots.
const snapshotQuery = "rdctl snapshot list --json"

func TestDocsReferenceWaitsForARespondingBackend(t *testing.T) {
	tools := &fakeTools{
		output: docsAnswers(),
		stderr: map[string]string{
			"rdctl list-settings": "Error: connection refused",
		},
	}

	answer, err := docsReferenceReady(context.Background(), docsRun(t, tools))
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, "not answering rdctl") {
		t.Errorf("a stopped backend answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestDocsReferenceBlocksWhileTheMachineHoldsSnapshots(t *testing.T) {
	answers := docsAnswers()
	answers["rdctl list-settings"] = "{}\n"
	answers[snapshotQuery] = `[{"name":"before-the-upgrade","created":"2026-09-19T18:23:00Z"}]` + "\n"

	answer, err := docsReferenceReady(context.Background(),
		docsRun(t, &fakeTools{output: answers}))
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, "holds snapshots") {
		t.Errorf("a machine with snapshots answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestDocsReferenceIsReadyOnACleanMachine(t *testing.T) {
	answers := docsAnswers()
	answers["rdctl list-settings"] = "{}\n"
	answers[snapshotQuery] = "\n"

	answer, err := docsReferenceReady(context.Background(),
		docsRun(t, &fakeTools{output: answers}))
	if err != nil {
		t.Fatal(err)
	}

	if !answer.OK {
		t.Errorf("a machine with no snapshots answered %v: %s", answer.OK, answer.Detail)
	}
}

func TestReferenceSummaryNamesABuildThatIsNotTheRelease(t *testing.T) {
	answers := referenceAnswers("")
	answers["rdctl version"] = "rdctl client version: v1.24.0-438-g46139d511, targeting server version: v1\n"

	summary, err := referenceSummary(context.Background(),
		docsRun(t, &fakeTools{output: answers}))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(summary, "v1.24.0-438-g46139d511") || !strings.Contains(summary, testRelease.Tag()) {
		t.Errorf("the summary named neither the build nor the release: %s", summary)
	}
}

func TestPlanDocsReferenceRunsTheScriptAndPushesToTheFork(t *testing.T) {
	run := docsRun(t, &fakeTools{output: referenceAnswers(string(documentedPage(t)))})
	run.Settings.DocsClone = t.TempDir()

	operations, err := planDocsReference(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	worktree, err := worktreePath(run, "docs")
	if err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(worktree, rdctlReferenceScript)
	shown := make([]string, 0, len(operations))

	for _, operation := range operations {
		shown = append(shown, operation.Description)
	}

	for _, want := range []string{
		script + " " + testRelease.String(),
		"git commit --signoff --message " + quote(referenceCommitMessage(testRelease)),
		"git push " + quote("https://github.com/"+testDocsFork+".git") +
			" HEAD:refs/heads/" + testBranch,
	} {
		if !slices.Contains(shown, want) {
			t.Errorf("the plan does not run %q; it runs %q", want, shown)
		}
	}

	// Every command of the action works on the checkout, not on whatever
	// directory the tool was started in.
	for _, operation := range operations {
		if operation.Command == "git" && strings.HasPrefix(operation.Description, "git fetch") {
			continue
		}

		if operation.Command != "" && operation.Dir != worktree {
			t.Errorf("%q runs in %q, want the worktree at %q",
				operation.Description, operation.Dir, worktree)
		}
	}
}

func TestDocsReferenceWaitsForTheBundledUtilities(t *testing.T) {
	answers := docsAnswers()
	// A release branch with no version file leaves the bundled utilities
	// step available, so the reference cannot go on top of it yet.
	delete(answers, fileQuery(testDocsFork, testBranch, testDocsFile))

	tools := &fakeTools{
		output: answers,
		stderr: map[string]string{
			fileQuery(testDocsFork, testBranch, testDocsFile): "gh: Not Found (HTTP 404)",
		},
	}

	answer, err := docsReferenceReady(context.Background(), docsRun(t, tools))
	if err != nil {
		t.Fatal(err)
	}

	if answer.OK || !strings.Contains(answer.Detail, docsUtilities.Title) {
		t.Errorf("an unwritten utilities step answered %v: %s", answer.OK, answer.Detail)
	}
}

// cleanMachineAnswers is every command 7b runs on a machine that is ready to
// regenerate the page, with the page reporting the release.
func cleanMachineAnswers(t *testing.T) map[string]string {
	t.Helper()

	page := strings.Replace(string(documentedPage(t)), "v1.24.0,", testRelease.Tag()+",", 1)

	answers := docsAnswers()
	maps.Copy(answers, referenceAnswers(page))
	answers["rdctl list-settings"] = "{}\n"
	answers[snapshotQuery] = "\n"

	return answers
}

func TestDocsReferenceWaitsForAMarkOnThePage(t *testing.T) {
	run := docsRun(t, &fakeTools{output: cleanMachineAnswers(t)})

	status := run.Status(context.Background(), docsReference)
	if status.State != Available || !strings.Contains(status.Detail, "nobody has marked it done") {
		t.Fatalf("an unmarked page was %s: %s", status.State, status.Detail)
	}

	if err := Mark(context.Background(), docsReference, run); err != nil {
		t.Fatal(err)
	}

	marked := docsRun(t, &fakeTools{output: cleanMachineAnswers(t)})
	marked.Confirmations = run.Confirmations

	if status := marked.Status(context.Background(), docsReference); status.State != Done {
		t.Errorf("a marked page was %s: %s", status.State, status.Detail)
	}
}

func TestRestoreHostSettingsPassesOverAFieldTheGenerationDropped(t *testing.T) {
	documented := documentedPage(t)
	generated := generatedPage(t, documented)
	without := strings.Replace(string(generated), `    "memoryInGB": 4,`+"\n", "", 1)

	restored, err := restoreHostSettings(documented, []byte(without))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(restored), "memoryInGB") {
		t.Error("a setting the generation dropped came back")
	}
}

func TestNormalizeReferenceReadsThePageAtTheCheckedOutCommit(t *testing.T) {
	documented := documentedPage(t)
	generated := generatedPage(t, documented)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rdctlReferencePage)), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, rdctlReferencePage), generated, 0o644); err != nil {
		t.Fatal(err)
	}

	tools := &fakeTools{output: map[string]string{
		"git show " + testHead + ":" + rdctlReferencePage: string(documented),
	}}

	run := docsRun(t, tools)
	if err := normalizeReference(context.Background(), run, dir, testHead); err != nil {
		t.Fatal(err)
	}

	page, err := os.ReadFile(filepath.Join(dir, rdctlReferencePage))
	if err != nil {
		t.Fatal(err)
	}

	changed := differingLines(string(documented), string(page))
	want := []string{"    " + generatedKubernetes + ","}

	if !slices.Equal(changed, want) {
		t.Errorf("normalizing left %q, want %q", changed, want)
	}
}
