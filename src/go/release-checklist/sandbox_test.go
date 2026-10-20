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
	"slices"
	"strings"
	"testing"
)

// The tests in this file perform the tool's actions with real git, against
// bare repositories under a temporary directory that stand in for a GitHub
// repository and the user's fork of it. The fake answers the clone's remote
// list and every command but git and the generation script, so no test
// reaches the network or runs the developer's rdctl, and
// GIT_ALLOW_PROTOCOL=file keeps git itself off the network. Unlike a fake
// commander, these tests see what a worktree holds after a failed attempt,
// what a commit takes with it, and which commit a push sends.

// realGit runs git and the generation script for real, and leaves the clone's
// remote list and every other command to the fake.
type realGit struct {
	fake *fakeTools
	env  []string
}

func (r *realGit) isReal(name string, args []string) bool {
	if name == "git" {
		return len(args) == 0 || args[0] != "remote"
	}

	return filepath.IsAbs(name) && strings.HasSuffix(name, string(filepath.Separator)+filepath.FromSlash(rdctlReferenceScript))
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

func (r *realGit) installed(name string) bool {
	return r.isReal(name, nil) || r.fake.installed(name)
}

// isolatedGitEnv is this process's environment for a git that does not
// read the system config, takes its global config from the file it is
// given, and looks for its attributes file under that file's directory, so
// the developer's settings cannot change what git does or prints.
func isolatedGitEnv(globalConfig string) []string {
	var env []string

	for _, setting := range os.Environ() {
		if !strings.HasPrefix(setting, "GIT_") && !strings.HasPrefix(setting, "XDG_CONFIG_HOME=") {
			env = append(env, setting)
		}
	}

	return append(env, "GIT_CONFIG_COUNT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+globalConfig,
		"XDG_CONFIG_HOME="+filepath.Dir(globalConfig))
}

// gitSandbox is a repository, a fork of it, and a clone of the repository,
// all under a temporary directory, with an environment whose git reaches the
// repository and the fork by their GitHub URLs.
type gitSandbox struct {
	env                   []string
	upstream, fork, clone string
	head                  string
}

func (s *gitSandbox) git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	return runGit(t, dir, s.env, args...)
}

// runGit runs git in dir and returns its trimmed output. A nil env passes on
// this process's environment.
func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir, cmd.Env = dir, env

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// inFork runs git against the bare fork.
func (s *gitSandbox) inFork(t *testing.T, args ...string) string {
	t.Helper()

	return s.git(t, "", append([]string{"--git-dir", s.fork}, args...)...)
}

// newGitSandbox seeds the repository and the fork with one commit of the
// files that populate writes into the seed directory, and puts the branches
// at it.
func newGitSandbox(t *testing.T, upstreamRepo, forkRepo string, populate func(seed string), branches ...string) *gitSandbox {
	t.Helper()

	root := t.TempDir()
	s := &gitSandbox{
		upstream: filepath.Join(root, "upstream.git"),
		fork:     filepath.Join(root, "fork.git"),
		clone:    filepath.Join(root, "clone"),
	}

	// The tool pushes and fetches by GitHub URL; git rewrites those to the
	// bare repositories here, and refuses every other protocol. git applies
	// the longest matching prefix, so each spelling that a remote or the
	// public URL can take gets a rule of its own.
	var config strings.Builder

	config.WriteString("[user]\n\tname = Test Person\n\temail = test@example.invalid\n")
	fmt.Fprintf(&config, "[init]\n\tdefaultBranch = %s\n", defaultBranch)

	for repo, path := range map[string]string{forkRepo: s.fork, upstreamRepo: s.upstream} {
		for _, suffix := range []string{"", "/", ".git"} {
			fmt.Fprintf(&config, "[url %q]\n\tinsteadOf = https://github.com/%s%s\n", path, repo, suffix)
		}
	}

	configFile := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(configFile, []byte(config.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	s.env = append(isolatedGitEnv(configFile), "GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0")

	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}

	populate(seed)

	s.git(t, seed, "init", "--quiet")
	s.git(t, seed, "add", ".")
	s.git(t, seed, "commit", "--quiet", "--message", "seed")

	for _, branch := range branches {
		s.git(t, seed, "branch", branch)
	}

	s.head = s.git(t, seed, "rev-parse", "HEAD")
	s.git(t, root, "clone", "--quiet", "--bare", seed, s.upstream)
	s.git(t, root, "clone", "--quiet", "--bare", seed, s.fork)
	s.git(t, root, "clone", "--quiet", s.upstream, s.clone)

	return s
}

// docsSandbox is a documentation repository, a fork of it, and a clone of the
// repository with the fork as origin, the way gh sets a contributor up.
type docsSandbox struct {
	*gitSandbox
}

func newDocsSandbox(t *testing.T) *docsSandbox {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the stand-in generation script needs sh")
	}

	files := map[string][]byte{
		docsReferencePage:  readTestdata(t, "bundled-utilities-1.24.0-after.md"),
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

	populate := func(seed string) {
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
	}

	return &docsSandbox{newGitSandbox(t, testDocsRepo, testDocsFork, populate)}
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

// nextRefresh is the run that the dashboard builds for the next key press. It
// has the same release, profile, clones and answers, with nothing read yet.
func nextRefresh(run *Run) *Run {
	repo := &repository{repo: run.Repo.repo, url: run.Repo.url, pushURL: run.Repo.pushURL, dir: run.Repo.dir, run: run.Repo.run}
	next := newRun(run.Release, run.Profile, repo)
	next.Refs = run.Refs
	next.Tools = run.Tools
	next.Confirmations = run.Confirmations
	next.Settings = run.Settings

	return next
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

	if content := s.inFork(t, "show", testBranch+":"+testDocsFile); content+"\n" != docsVersionFileText {
		t.Errorf("the pushed version file holds\n%s\nwant\n%s", content, docsVersionFileText)
	}

	page := s.inFork(t, "show", testBranch+":"+docsReferencePage)
	for _, want := range []string{
		"import Version123 from '../bundled-utilities-version-info/v1.23.0.md';",
		"import Version124 from '../bundled-utilities-version-info/v1.24.0.md';",
		"import Version125 from '../bundled-utilities-version-info/v1.25.0.md';",
		"| v1.25.0                 | <Version125 />      |",
		"| v1.24.0                 | <Version124 />      |",
		"| v1.23.0                 | <Version123 />      |",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the pushed reference page lacks %q", want)
		}
	}

	if strings.Contains(page, "Version122") {
		t.Error("the pushed reference page still lists v1.22.0")
	}

	forkBranchExists(t, s, tools)
	run = nextRefresh(run)

	if operations, err = planDocsReference(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	page = s.inFork(t, "show", testBranch+":"+rdctlReferencePage)
	for _, want := range []string{`"memoryInGB": 6`, "client version: v1.25.0"} {
		if !strings.Contains(page, want) {
			t.Errorf("the pushed page lacks %q", want)
		}
	}

	if status := s.git(t, s.clone, "status", "--short", "--branch"); status != "## main...origin/main" {
		t.Errorf("the user's clone changed:\n%s", status)
	}
}

func TestTheDocsActionsRefuseARepositoryThatIsNotTheFork(t *testing.T) {
	s := newDocsSandbox(t)
	run, tools := sandboxRun(t, s)
	tools.output["gh api repos/"+testDocsFork+" --jq .parent.full_name"] = "someone/docs.rancherdesktop.io\n"
	// The docs repository can be pushed to, has no release branch yet, and
	// has a push URL, so only the fork lookup keeps the action from pushing
	// there.
	tools.output[permissionsQuery(testDocsRepo)] = `{"pull":true,"push":true}`
	tools.stderr[headQuery(testDocsRepo, testBranch)] = "gh: Branch not found (HTTP 404)"
	tools.output["git remote --verbose"] = strings.Replace(docsRemoteOutput,
		"DISABLED", "https://github.com/"+testDocsRepo, 1)

	operations, err := planDocsUtilities(context.Background(), run)
	if err == nil {
		err = perform(run, operations)
	}

	if err == nil {
		t.Error("the action succeeded with no fork to push to")
	}

	if refs := s.git(t, s.upstream, "for-each-ref", "refs/heads/"+testBranch); refs != "" {
		t.Errorf("the action pushed to the docs repository:\n%s", refs)
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
	run = nextRefresh(run)

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
	run = nextRefresh(run)

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
		run = nextRefresh(run)

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

// sandboxUpstream is the release repository that the code sandbox stands in
// for, under a name other than production's, so that a command that gets past
// the fake cannot reach production. The fork is testRepo.
const sandboxUpstream = "rancher/rancher-desktop"

// sandboxRemoteOutput is what the fake answers for `git remote --verbose` in
// the tool's own clone, with the fork as origin and the release repository as
// upstream.
const sandboxRemoteOutput = "origin\thttps://github.com/" + testRepo + ".git (fetch)\n" +
	"origin\thttps://github.com/" + testRepo + ".git (push)\n" +
	"upstream\thttps://github.com/" + sandboxUpstream + ".git (fetch)\n" +
	"upstream\thttps://github.com/" + sandboxUpstream + ".git (push)\n"

// newCodeSandbox is a release repository whose release branch has the
// previous release's package.json, a fork of it, and the tool's own clone.
// Step 4 works in the tool's own clone, the current directory, so the test
// moves there and keeps the release's worktrees in a cache of its own.
func newCodeSandbox(t *testing.T) *gitSandbox {
	t.Helper()

	populate := func(seed string) {
		if err := os.WriteFile(filepath.Join(seed, "package.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := newGitSandbox(t, sandboxUpstream, testRepo, populate, testBranch)

	t.Chdir(s.clone)
	inACacheOfItsOwn(t)

	return s
}

// bumpRun reads the test release afresh, as the dashboard does before it runs
// a step's action, with git real and gh answered by the fake.
func bumpRun(t *testing.T, s *gitSandbox, fake *fakeTools, extraEnv ...string) *Run {
	t.Helper()

	tools := &realGit{fake: fake, env: append(slices.Clone(s.env), extraEnv...)}

	repo, err := newRepository(t.Context(), "", sandboxUpstream, tools)
	if err != nil {
		t.Fatal(err)
	}

	run := newRun(&Release{Version: testRelease, Kind: Minor},
		&Profile{Name: "sandbox", GitHub: GitHubResources{Repo: sandboxUpstream}}, repo)
	run.Tools = tools
	run.Refs = &Refs{Branches: map[Line]string{testRelease.Line(): s.head}, Tags: map[Version]string{}}

	return run
}

func TestTheVersionBumpRetriesAfterAFailedPullRequest(t *testing.T) {
	s := newCodeSandbox(t)

	prCreate := "gh pr create --repo " + sandboxUpstream + " --base " + testBranch +
		" --head " + testLogin + ":bump-to-1.25.0 --title Bump version to 1.25.0" +
		" --body The release branch carries the version it ships."
	forkPackage := fileQuery(testRepo, "bump-to-1.25.0", "package.json")

	fake := &fakeTools{
		output: map[string]string{
			"git remote --verbose":                                 sandboxRemoteOutput,
			permissionsQuery(sandboxUpstream):                      `{"pull":true}`,
			permissionsQuery(testRepo):                             `{"push":true}`,
			"gh api user --jq .login":                              testLogin + "\n",
			"gh api repos/" + testRepo + " --jq .parent.full_name": sandboxUpstream + "\n",
			fileQuery(sandboxUpstream, testBranch, "package.json"): manifest,
			"gh api repos/" + sandboxUpstream + "/pulls?state=open&head=" + testLogin +
				":bump-to-1.25.0 --jq .[0].number": "\n",
		},
		stderr: map[string]string{
			forkPackage: "gh: No commit found for the ref bump-to-1.25.0 (HTTP 404)",
			prCreate:    "pull request create failed: GraphQL: was submitted too quickly (createPullRequest)",
		},
	}

	// Each attempt commits at a date of its own. A retry within the same
	// second would make the same commit again, whose push changes nothing.
	first := bumpRun(t, s, fake, "GIT_AUTHOR_DATE=2030-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2030-01-01T00:00:00Z")
	if err := RunAction(context.Background(), versionBump, first, strings.NewReader("y\n"), io.Discard); err == nil {
		t.Fatal("the failed pull request was reported as success")
	}

	pushed := s.inFork(t, "rev-parse", "bump-to-1.25.0")
	if content := s.inFork(t, "show", "bump-to-1.25.0:package.json"); !strings.Contains(content, `"version": "1.25.0"`) {
		t.Fatalf("the fork's bump-to-1.25.0 holds\n%s", content)
	}

	// gh answers what it would after the push, and opens the pull request.
	delete(fake.stderr, forkPackage)
	fake.output[forkPackage] = s.inFork(t, "show", "bump-to-1.25.0:package.json")
	delete(fake.stderr, prCreate)
	fake.output[prCreate] = "https://github.com/" + sandboxUpstream + "/pull/1\n"

	second := bumpRun(t, s, fake, "GIT_AUTHOR_DATE=2030-01-01T00:10:00Z", "GIT_COMMITTER_DATE=2030-01-01T00:10:00Z")

	var out strings.Builder
	if err := RunAction(context.Background(), versionBump, second, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("the retry failed: %v\n%s", err, out.String())
	}

	if head := s.inFork(t, "rev-parse", "bump-to-1.25.0"); head != pushed {
		t.Errorf("the fork's bump-to-1.25.0 moved from %.7s to %.7s", pushed, head)
	}

	if !containsCall(fake.calls, prCreate) {
		t.Errorf("the retry did not open the pull request; it ran\n%s", strings.Join(fake.calls, "\n"))
	}
}

func TestAWorktreeStartsFromThePlannedCommitWhateverTheLastAttemptLeft(t *testing.T) {
	s := newGitSandbox(t, sandboxUpstream, testRepo, func(seed string) {
		if err := os.WriteFile(filepath.Join(seed, "file"), []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})

	first := s.head
	if err := os.WriteFile(filepath.Join(s.clone, "file"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.git(t, s.clone, "commit", "--quiet", "--all", "--message", "second")
	second := s.git(t, s.clone, "rev-parse", "HEAD")

	run := &Run{Tools: &realGit{fake: &fakeTools{}, env: s.env}}
	path := filepath.Join(t.TempDir(), "cache", "worktree")

	checkOut := func(commit string) {
		t.Helper()

		if err := worktreeAt(t.Context(), run, s.clone, path, commit); err != nil {
			t.Fatal(err)
		}

		if head := s.git(t, path, "rev-parse", "HEAD"); head != commit {
			t.Errorf("the worktree is at %.7s, want %.7s", head, commit)
		}
	}

	checkOut(first)

	// A failed attempt leaves a new file and an edited one behind.
	untracked := filepath.Join(path, "untracked")
	for _, file := range []string{untracked, filepath.Join(path, "file")} {
		if err := os.WriteFile(file, []byte("failed attempt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	checkOut(second)

	if status := s.git(t, path, "status", "--porcelain"); status != "" {
		t.Errorf("the reused worktree holds the failed attempt's changes:\n%s", status)
	}

	if _, err := os.Stat(untracked); !os.IsNotExist(err) {
		t.Errorf("the failed attempt's untracked file survived: %v", err)
	}

	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	checkOut(first)
}

// advanceUpstream adds an empty commit to a branch of the bare upstream and
// returns it.
func advanceUpstream(t *testing.T, s *gitSandbox, branch string) string {
	t.Helper()

	commit := s.git(t, "", "--git-dir", s.upstream, "commit-tree", "-p", branch, "-m", "moved", branch+"^{tree}")
	s.git(t, "", "--git-dir", s.upstream, "update-ref", "refs/heads/"+branch, commit)

	return commit
}

// upstreamRef is the commit that a ref of the bare upstream points at, or
// nothing.
func upstreamRef(t *testing.T, s *gitSandbox, ref string) string {
	t.Helper()

	return s.git(t, "", "--git-dir", s.upstream, "for-each-ref", "--format=%(objectname)", ref)
}

func TestTheReleaseBranchStartsAtTheCommitTheSummaryShowed(t *testing.T) {
	s := newCodeSandbox(t)
	// Step 1 creates the release branch.
	s.git(t, "", "--git-dir", s.upstream, "update-ref", "-d", "refs/heads/"+testBranch)

	// main moves on past the commit that the summary showed, and the clone
	// has neither.
	shown := advanceUpstream(t, s, defaultBranch)
	latest := advanceUpstream(t, s, defaultBranch)

	run := bumpRun(t, s, &fakeTools{output: map[string]string{
		"git remote --verbose": sandboxRemoteOutput,
		"gh api repos/" + sandboxUpstream + "/branches/main": `{"name":"main","commit":{"sha":"` + shown +
			`","commit":{"committer":{"date":"2026-09-01T12:00:00Z"},"message":"shown"}}}`,
	}})

	operations, err := planReleaseBranch(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	if branch := upstreamRef(t, s, "refs/heads/"+testBranch); branch != shown {
		t.Errorf("%s is at %q, want %.7s, which the summary showed (main is at %.7s)", testBranch, branch, shown, latest)
	}
}

func TestTheTagGoesOnTheCommitTheRefreshRead(t *testing.T) {
	s := newCodeSandbox(t)

	// The release branch moves on past the commit that the refresh read, and
	// the clone has neither.
	read := advanceUpstream(t, s, testBranch)
	latest := advanceUpstream(t, s, testBranch)

	run := bumpRun(t, s, &fakeTools{output: map[string]string{"git remote --verbose": sandboxRemoteOutput}})
	run.Refs.Branches[testRelease.Line()] = read

	operations, err := planTag(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := perform(run, operations); err != nil {
		t.Fatal(err)
	}

	if tag := upstreamRef(t, s, "refs/tags/"+testRelease.Tag()); tag != read {
		t.Errorf("%s is at %q, want %.7s, which the refresh read (%s is at %.7s)", testRelease.Tag(), tag, read, testBranch, latest)
	}
}
