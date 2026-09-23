// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests in this file perform the docs actions with real git, against
// bare repositories under a temporary directory that stand in for the
// documentation repository and the user's fork. The fake still answers gh and
// the clone's remote list, so nothing reaches the network, and
// GIT_ALLOW_PROTOCOL=file makes sure of it. They cover what a fake commander
// cannot: what a worktree holds after a failed attempt, and what a commit
// takes with it.

// realGit runs git and the generation script for real, and leaves gh and the
// clone's remote list to the fake.
type realGit struct {
	fake *fakeTools
	env  []string
}

func (r *realGit) isReal(name string, args []string) bool {
	return name != "gh" && (name != "git" || len(args) == 0 || args[0] != "remote")
}

func (r *realGit) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.runIn(ctx, "", name, args...)
}

func (r *realGit) runIn(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if !r.isReal(name, args) {
		return r.fake.runIn(ctx, dir, name, args...)
	}

	var out, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, r.env, &out, &stderr

	if err := cmd.Run(); err != nil {
		return out.Bytes(), &commandFailure{Command: name + " " + strings.Join(args, " "), Stderr: stderr.String(), Err: err}
	}

	return out.Bytes(), nil
}

func (r *realGit) runTo(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	if !r.isReal(name, args) {
		return r.fake.runTo(ctx, dir, out, name, args...)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, r.env, out, out

	return cmd.Run()
}

func (r *realGit) installed(string) bool { return true }

// docsSandbox is a documentation repository, a fork of it, and a clone of the
// repository with the fork as origin, the way gh sets a contributor up.
type docsSandbox struct {
	env                   []string
	upstream, fork, clone string
	head                  string
}

func (s *docsSandbox) git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir, cmd.Env = dir, s.env

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// inFork runs git against the bare fork.
func (s *docsSandbox) inFork(t *testing.T, args ...string) string {
	t.Helper()

	return s.git(t, "", append([]string{"--git-dir", s.fork}, args...)...)
}

func newDocsSandbox(t *testing.T) *docsSandbox {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the stand-in generation script needs sh")
	}

	root := t.TempDir()
	s := &docsSandbox{
		upstream: filepath.Join(root, "upstream.git"),
		fork:     filepath.Join(root, "fork.git"),
		clone:    filepath.Join(root, "clone"),
	}

	// The tool pushes and fetches by GitHub URL; git rewrites those to the
	// bare repositories here, and refuses every other protocol.
	config := fmt.Sprintf("[user]\n\tname = Test Person\n\temail = test@example.invalid\n"+
		"[url %q]\n\tinsteadOf = https://github.com/%s/\n"+
		"[url %q]\n\tinsteadOf = https://github.com/%s\n"+
		"[init]\n\tdefaultBranch = %s\n", s.fork, testDocsFork, s.upstream, testDocsRepo, defaultBranch)
	configFile := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(configFile, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, setting := range os.Environ() {
		if !strings.HasPrefix(setting, "GIT_") {
			s.env = append(s.env, setting)
		}
	}

	s.env = append(s.env, "GIT_CONFIG_COUNT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+configFile,
		"GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0")

	seed := filepath.Join(root, "seed")
	files := map[string][]byte{
		docsReferencePage:  readTestdata(t, "bundled-utilities-after.md"),
		rdctlReferencePage: readTestdata(t, "rdctl-command-reference.md"),
		// Stands in for the generation, on a machine configured unlike the
		// one that wrote the page.
		rdctlReferenceScript: []byte("#!/bin/sh\nset -e\ncd \"$(dirname \"$0\")/..\"\n" +
			"perl -0pi -e \"s/\\\"memoryInGB\\\": 6/\\\"memoryInGB\\\": 4/; s/client version: v1.24.0/client version: v$1/\" " +
			rdctlReferencePage + "\n"),
	}

	for _, version := range []string{"1.22.0", "1.23.0", "1.24.0"} {
		files[filepath.Join(docsVersionDir, "v"+version+".md")] = []byte("docker: " + version + " <br/>\n")
	}

	for name, content := range files {
		path := filepath.Join(seed, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		mode := os.FileMode(0o644)
		if name == rdctlReferenceScript {
			mode = 0o755
		}

		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
	}

	s.git(t, seed, "init", "--quiet")
	s.git(t, seed, "add", ".")
	s.git(t, seed, "commit", "--quiet", "--message", "seed")
	s.head = s.git(t, seed, "rev-parse", "HEAD")
	s.git(t, root, "clone", "--quiet", "--bare", seed, s.upstream)
	s.git(t, root, "clone", "--quiet", "--bare", seed, s.fork)
	s.git(t, root, "clone", "--quiet", s.upstream, s.clone)

	return s
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}

	return content
}

// sandboxRun is a docs refresh whose git commands reach the sandbox.
func sandboxRun(t *testing.T, s *docsSandbox) (*Run, *fakeTools) {
	t.Helper()

	run, tools := docsPlanRun(t, s.clone)
	tools.output[headQuery(testDocsRepo, defaultBranch)] = s.head + "\n"
	run.Tools = &realGit{fake: tools, env: s.env}

	return run, tools
}

// forkBranchExists tells the fake that the release branch is now on the fork,
// the way gh would answer after a push.
func forkBranchExists(t *testing.T, s *docsSandbox, tools *fakeTools) {
	t.Helper()

	delete(tools.stderr, headQuery(testDocsFork, testBranch))
	tools.output[headQuery(testDocsFork, testBranch)] = s.inFork(t, "rev-parse", testBranch) + "\n"
}

func perform(run *Run, operations []Operation) error {
	var out bytes.Buffer

	for _, operation := range operations {
		if err := operation.perform(context.Background(), run, &out); err != nil {
			return fmt.Errorf("%s: %w\n%s", operation.Description, err, out.String())
		}
	}

	return nil
}

// refuseCommits installs a hook in the clone that refuses every commit while
// the marker exists, so a test can make one attempt fail after staging.
func refuseCommits(t *testing.T, s *docsSandbox) (release func()) {
	t.Helper()

	marker := filepath.Join(t.TempDir(), "refuse")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	hook := "#!/bin/sh\n[ -e " + marker + " ] && { echo hook refused >&2; exit 1; }\nexit 0\n"
	if err := os.WriteFile(filepath.Join(s.clone, ".git", "hooks", "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}

	return func() { os.Remove(marker) }
}

func TestTheDocsActionsPushToTheForkAndLeaveTheCloneAlone(t *testing.T) {
	s := newDocsSandbox(t)
	run, tools := sandboxRun(t, s)

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	listed := s.inFork(t, "ls-tree", "--name-only", testBranch+":"+docsVersionDir)
	if want := "v1.23.0.md\nv1.24.0.md\nv1.25.0.md"; listed != want {
		t.Errorf("the fork's version directory lists\n%s\nwant\n%s", listed, want)
	}

	forkBranchExists(t, s, tools)

	if operations, err = planDocsReference(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	page := s.inFork(t, "show", testBranch+":"+rdctlReferencePage)
	for _, want := range []string{`"memoryInGB": 6`, "client version: v1.25.0"} {
		if !strings.Contains(page, want) {
			t.Errorf("the pushed page lacks %q", want)
		}
	}

	if status := s.git(t, s.clone, "status", "--short", "--branch"); status != "## main...origin/main" {
		t.Errorf("the user's clone changed:\n%s", status)
	}
}

func TestADocsActionRetriesAfterAFailedCommit(t *testing.T) {
	s := newDocsSandbox(t)
	run, _ := sandboxRun(t, s)

	release := refuseCommits(t, s)

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err == nil {
		t.Fatal("the refused commit was reported as success")
	}

	release()

	// The worktree still holds the failed attempt's changes; the retry has to
	// start from the planned commit again.
	if operations, err = planDocsUtilities(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
}

func TestAStagedPageFromAFailedRunStaysOutOfTheNextCommit(t *testing.T) {
	s := newDocsSandbox(t)
	run, tools := sandboxRun(t, s)

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	forkBranchExists(t, s, tools)
	tools.output[listQuery(testDocsFork, testBranch, docsVersionDir)] = "v1.23.0.md\nv1.24.0.md\nv1.25.0.md\n"

	release := refuseCommits(t, s)

	if operations, err = planDocsReference(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err == nil {
		t.Fatal("the refused commit was reported as success")
	}

	release()

	// A utility moves, so the bundled utilities step runs again.
	tools.output[fileQuery(testRepo, testBranch, dependenciesFile)] = strings.Replace(docsDependencies, "4.3.0", "4.3.1", 1)

	if operations, err = planDocsUtilities(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	changed := s.inFork(t, "show", "--name-only", "--format=", testBranch)
	if strings.Contains(changed, rdctlReferencePage) {
		t.Errorf("the bundled utilities commit took the staged page with it:\n%s", changed)
	}
}

func TestRegeneratingTheReferenceFromTheSameBuildPushesNothingNew(t *testing.T) {
	s := newDocsSandbox(t)
	run, tools := sandboxRun(t, s)

	operations, err := planDocsUtilities(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	// The step stays available until somebody marks the page, so the action
	// can run again after its push and regenerate the page it pushed.
	for range 2 {
		forkBranchExists(t, s, tools)

		if operations, err = planDocsReference(context.Background(), run); err != nil {
			t.Fatal(err)
		}

		if err := perform(run, operations); err != nil {
			t.Fatal(err)
		}
	}

	subjects := s.inFork(t, "log", "--format=%s", testBranch)
	if commits := strings.Count(subjects, referenceCommitMessage(testRelease)); commits != 1 {
		t.Errorf("the fork has %d reference commits:\n%s", commits, subjects)
	}
}
