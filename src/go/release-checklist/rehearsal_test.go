// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResetRehearsalRefusesTheRealRelease runs the reset script against
// profiles that name the real release. It must refuse before gh runs, and it
// has to find the profile where the tool itself looks for it.
func TestResetRehearsalRefusesTheRealRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	tests := map[string]struct{ profile, yaml, refusal string }{
		"the production profile": {
			profile: "Production",
			refusal: "resets rehearsals only",
		},
		"the release repository": {
			profile: "rehearsal",
			yaml:    "name: rehearsal\ngithub:\n  repo: rancher-sandbox/rancher-desktop\n",
			refusal: "names rancher-sandbox/rancher-desktop",
		},
		"the documentation repository": {
			profile: "rehearsal",
			yaml: "name: rehearsal\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n" +
				"  docsRepo: Rancher-Sandbox/docs.rancherdesktop.io\n",
			refusal: "names Rancher-Sandbox/docs.rancherdesktop.io",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_CACHE_HOME", "")

			if test.yaml != "" {
				writeProfile(t, test.profile, test.yaml)
			}

			// A gh that says it ran, so a refusal that comes too late shows.
			output, err := resetRehearsal(t, test.profile, "echo gh ran >&2\nexit 99\n")
			if err == nil || !strings.Contains(string(output), test.refusal) || strings.Contains(string(output), "gh ran") {
				t.Errorf("reset-rehearsal.sh %s exited with %v:\n%s", test.profile, err, output)
			}
		})
	}
}

// TestResetRehearsalRefusesARepositoryThatIsNotTheFork runs the reset script
// for a user whose repository of the rehearsal's name is a fork of something
// else. It must stop there, before it lists or deletes anything.
func TestResetRehearsalRefusesARepositoryThatIsNotTheFork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	writeProfile(t, "rehearsal", "name: rehearsal\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n")

	output, err := resetRehearsal(t, "rehearsal", `case "$*" in
"api user --jq .login") echo me ;;
"api repos/me/rancher-desktop-rehearsal --jq .parent.full_name") echo someone/rancher-desktop-rehearsal ;;
*) echo "gh ran: $*" >&2; exit 99 ;;
esac
`)

	refusal := "me/rancher-desktop-rehearsal is not a fork of example-org/rancher-desktop-rehearsal"
	if err == nil || !strings.Contains(string(output), refusal) || strings.Contains(string(output), "gh ran") {
		t.Errorf("reset-rehearsal.sh rehearsal exited with %v:\n%s", err, output)
	}
}

// writeProfile writes a profile where the tool looks for it.
func writeProfile(t *testing.T, profile, yaml string) {
	t.Helper()

	dir, err := ProfileDir(profile)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "profile.yaml"), yaml)
}

// resetRehearsal runs the reset script with a gh whose body is the given
// shell, first on PATH.
func resetRehearsal(t *testing.T, profile, gh string) ([]byte, error) {
	t.Helper()

	installGH(t, gh)

	return exec.CommandContext(t.Context(), "bash", "reset-rehearsal.sh", profile).CombinedOutput()
}

// installGH puts a gh whose body is the given shell, first on PATH.
func installGH(t *testing.T, gh string) {
	t.Helper()

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+gh), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// rehearsalGH answers the reads that a reset of line 1.99 makes and accepts
// the writes. It matches each read with its --jq filter and answers already
// filtered, so a read with a changed filter goes unanswered. The user has not
// forked the docs repository. It logs every call to $GH_LOG.
const rehearsalGH = `printf '%s\n' "$*" >> "$GH_LOG"
case "$*" in
'api user --jq .login') echo me ;;
'api repos/me/rancher-desktop-rehearsal --jq .parent.full_name') echo Example-Org/rancher-desktop-rehearsal ;;
'api repos/me/docs-rehearsal --jq .parent.full_name') echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
'pr list --repo example-org/rancher-desktop-rehearsal --state open --base release-1.99 --json number --jq .[].number') echo 12 ;;
'pr list --repo example-org/docs-rehearsal --state open --head release-1.99 --json number --jq .[].number') printf '7\n8\n' ;;
'release list --repo example-org/rancher-desktop-rehearsal --limit 1000 --json tagName --jq .[].tagName | select(startswith("v1.99.") or startswith("burned-v1.99."))')
	printf 'v1.99.1\nburned-v1.99.0\nv1.99.0\n' ;;
'api repos/example-org/rancher-desktop-rehearsal/git/matching-refs/tags/v1.99. --jq .[].ref')
	printf 'refs/tags/v1.99.0\nrefs/tags/v1.99.1\n' ;;
'api repos/example-org/rancher-desktop-rehearsal/git/matching-refs/heads/release-1.99 --jq .[].ref | select(. == "refs/heads/release-1.99")')
	echo refs/heads/release-1.99 ;;
'api repos/me/rancher-desktop-rehearsal/git/matching-refs/heads/bump-to-1.99. --jq .[].ref')
	printf 'refs/heads/bump-to-1.99.0\nrefs/heads/bump-to-1.99.1\n' ;;
'api repos/example-org/docs-rehearsal/git/matching-refs/heads/release-1.99 --jq .[].ref | select(. == "refs/heads/release-1.99")')
	echo refs/heads/release-1.99 ;;
'pr close '* | 'release delete '* | 'api --method DELETE '*) ;;
*) echo "stand-in gh: no answer for: $*" >&2; exit 98 ;;
esac
`

// TestResetRehearsalDeletesOnlyTheRehearsalsLine resets line 1.99 in a
// sandbox that also holds line 1.990, line 1.98 and a production profile.
func TestResetRehearsalDeletesOnlyTheRehearsalsLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	sandbox := newResetSandbox(t)
	installGH(t, rehearsalGH)

	output, err := sandbox.run(t, "y\n")
	if err != nil {
		t.Fatalf("reset-rehearsal.sh rehearsal exited with %v:\n%s", err, output)
	}

	if strings.Contains(string(output), "rehearsal rehearsal") {
		t.Errorf("the listing says \"rehearsal rehearsal\":\n%s", output)
	}

	repo, fork, docs := "example-org/rancher-desktop-rehearsal", "me/rancher-desktop-rehearsal", "example-org/docs-rehearsal"
	want := []string{
		"pr close 12 --repo " + repo,
		"pr close 7 --repo " + docs,
		"pr close 8 --repo " + docs,
		"release delete v1.99.1 --repo " + repo + " --yes",
		"release delete burned-v1.99.0 --repo " + repo + " --yes",
		"release delete v1.99.0 --repo " + repo + " --yes",
		"api --method DELETE repos/" + repo + "/git/refs/tags/v1.99.0 --silent",
		"api --method DELETE repos/" + repo + "/git/refs/tags/v1.99.1 --silent",
		"api --method DELETE repos/" + repo + "/git/refs/heads/release-1.99 --silent",
		"api --method DELETE repos/" + fork + "/git/refs/heads/bump-to-1.99.0 --silent",
		"api --method DELETE repos/" + fork + "/git/refs/heads/bump-to-1.99.1 --silent",
		"api --method DELETE repos/" + docs + "/git/refs/heads/release-1.99 --silent",
	}
	if got := sandbox.writes(t); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("gh writes:\n%s\nwant:\n%s\noutput:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), output)
	}

	for _, clone := range []string{sandbox.code, sandbox.docs} {
		tags := runGit(t, clone, nil, "tag", "--list")
		if tags != "v1.98.0\nv1.990.0" {
			t.Errorf("%s keeps the tags:\n%s", clone, tags)
		}

		if worktrees := runGit(t, clone, nil, "worktree", "list", "--porcelain"); strings.Count(worktrees, "worktree ") != 1 {
			t.Errorf("%s keeps worktrees:\n%s", clone, worktrees)
		}
	}

	for _, gone := range []string{sandbox.cache, filepath.Join(sandbox.config, "confirmations.yaml")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}

	for _, kept := range sandbox.production {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("the production profile lost %s: %v", kept, err)
		}
	}
}

// TestResetRehearsalTakesYesForAnAnswer answers the question with Yes, which
// the tool's own prompts accept.
func TestResetRehearsalTakesYesForAnAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	sandbox := newResetSandbox(t)
	installGH(t, rehearsalGH)

	output, err := sandbox.run(t, "Yes\n")
	if writes := sandbox.writes(t); err != nil || len(writes) == 0 {
		t.Errorf("answered Yes, reset-rehearsal.sh exited with %v after %d gh writes:\n%s", err, len(writes), output)
	}
}

// TestResetRehearsalDeletesNothingUnlessTheUserAgrees answers the question
// with end of input.
func TestResetRehearsalDeletesNothingUnlessTheUserAgrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	sandbox := newResetSandbox(t)
	installGH(t, rehearsalGH)

	output, err := sandbox.run(t, "")
	if err == nil || !strings.Contains(string(output), "Nothing was deleted.") {
		t.Errorf("reset-rehearsal.sh rehearsal exited with %v:\n%s", err, output)
	}

	if got := sandbox.writes(t); len(got) != 0 {
		t.Errorf("gh writes without a yes:\n%s", strings.Join(got, "\n"))
	}

	if tags := runGit(t, sandbox.code, nil, "tag", "--list"); tags != "v1.98.0\nv1.99.0\nv1.99.1\nv1.990.0" {
		t.Errorf("%s has only the tags:\n%s", sandbox.code, tags)
	}
}

// TestResetRehearsalStopsWhenTheForkLookupFails has GitHub fail to say
// whether the user forked the docs repository. Only a 404 means "no fork";
// taking any other failure for one would delete the docs repository's branch
// instead of the fork's.
func TestResetRehearsalStopsWhenTheForkLookupFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reset-rehearsal.sh runs on macOS and Linux only")
	}

	sandbox := newResetSandbox(t)
	installGH(t, strings.Replace(rehearsalGH, "gh: Not Found (HTTP 404)", "gh: Server Error (HTTP 500)", 1))

	output, err := sandbox.run(t, "y\n")
	refusal := "Looking for your fork of example-org/docs-rehearsal: gh: Server Error (HTTP 500)"
	if err == nil || !strings.Contains(string(output), refusal) || strings.Contains(string(output), "Go ahead?") {
		t.Errorf("reset-rehearsal.sh rehearsal exited with %v:\n%s", err, output)
	}

	if got := sandbox.writes(t); len(got) != 0 {
		t.Errorf("gh writes after a failed lookup:\n%s", strings.Join(got, "\n"))
	}
}

// resetSandbox is a rehearsal profile with a code clone and a docs clone, each
// with a worktree in the profile's cache, beside a production profile.
type resetSandbox struct {
	code, docs, config, cache, log string
	production                     []string
}

// newResetSandbox builds a sandbox under a temporary HOME. The script resets
// the clone that holds it, so it runs from a copy in the sandbox's code clone,
// never from this checkout.
func newResetSandbox(t *testing.T) *resetSandbox {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	s := &resetSandbox{
		code:  filepath.Join(home, "rancher-desktop"),
		docs:  filepath.Join(home, "docs"),
		cache: filepath.Join(cacheRoot, storeDir, "rehearsal"),
		log:   filepath.Join(home, "gh.log"),
	}
	t.Setenv("GH_LOG", s.log)

	writeProfile(t, "rehearsal", "name: rehearsal\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n"+
		"  docsRepo: example-org/docs-rehearsal\n")
	if s.config, err = ProfileDir("rehearsal"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.config, "settings.yaml"), "docsClone: "+s.docs+"\n")
	writeFile(t, filepath.Join(s.config, "confirmations.yaml"), "{}\n")

	writeProfile(t, "production", "name: production\ngithub:\n  repo: rancher-sandbox/rancher-desktop\n")
	productionConfig, err := ProfileDir("production")
	if err != nil {
		t.Fatal(err)
	}
	s.production = []string{
		filepath.Join(productionConfig, "profile.yaml"),
		filepath.Join(productionConfig, "confirmations.yaml"),
		filepath.Join(cacheRoot, storeDir, "production", "1.99.0", "download"),
	}
	for _, file := range s.production[1:] {
		writeFile(t, file, "kept\n")
	}

	for _, clone := range []string{s.code, s.docs} {
		runGit(t, "", nil, "init", "--quiet", clone)
		runGit(t, clone, nil, "-c", "user.name=Test", "-c", "user.email=test@example.com",
			"commit", "--quiet", "--allow-empty", "--message", "empty")
		for _, tag := range []string{"v1.99.0", "v1.99.1", "v1.98.0", "v1.990.0"} {
			runGit(t, clone, nil, "tag", tag)
		}
		runGit(t, clone, nil, "worktree", "add", "--quiet", "--detach",
			filepath.Join(s.cache, "1.99.0", filepath.Base(clone)))
	}

	// RESET_REHEARSAL_SCRIPT names a mutant of the script to run instead.
	script := os.Getenv("RESET_REHEARSAL_SCRIPT")
	if script == "" {
		script = "reset-rehearsal.sh"
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.code, "reset-rehearsal.sh"), string(body))

	return s
}

// run runs the sandbox's copy of the script with the given input.
func (s *resetSandbox) run(t *testing.T, stdin string) ([]byte, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(s.code, "reset-rehearsal.sh"), "rehearsal")
	cmd.Stdin = strings.NewReader(stdin)

	return cmd.CombinedOutput()
}

// writes returns the logged gh calls that change something on GitHub.
func (s *resetSandbox) writes(t *testing.T) []string {
	t.Helper()

	log, err := os.ReadFile(s.log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	var writes []string
	for _, call := range strings.Split(string(log), "\n") {
		for _, prefix := range []string{"pr close ", "release delete ", "api --method DELETE "} {
			if strings.HasPrefix(call, prefix) {
				writes = append(writes, call)
			}
		}
	}

	return writes
}

// writeFile writes a file, making its directory first.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
