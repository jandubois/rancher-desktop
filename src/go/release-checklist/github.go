// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// defaultBranch is the branch a release is merged back into, and the one a
// new line branches from.
const defaultBranch = "main"

// errNotFound is GitHub answering that a branch, file or release is not
// there, which is an answer a check reads rather than a failure. A lookup
// wraps it with what it looked for, so the error a caller passes on names
// the missing file, branch or release.
var errNotFound = errors.New("not found")

// ReleaseState is what GitHub knows about the release for a tag.
type ReleaseState string

const (
	// ReleaseMissing means no release names the tag.
	ReleaseMissing ReleaseState = "missing"
	// ReleaseDraft means the release exists but only maintainers see it.
	ReleaseDraft ReleaseState = "draft"
	// ReleasePublished means the release is out.
	ReleasePublished ReleaseState = "published"
)

// WorkflowRun is one run of a GitHub Actions workflow.
type WorkflowRun struct {
	// ID names the run to gh, which takes it as a string.
	ID string
	// Status is what the run is doing: queued, in_progress or completed.
	Status string
	// Conclusion is how a run that completed ended: success, failure,
	// cancelled, skipped or timed_out. It is empty until then.
	Conclusion string
	// URL is the run's page, for the line beside a step waiting on it.
	URL string
}

// Finished reports whether the run has stopped, however it ended.
func (w *WorkflowRun) Finished() bool { return w.Status == "completed" }

// Succeeded reports whether every job of the run passed.
func (w *WorkflowRun) Succeeded() bool { return w.Conclusion == "success" }

// State is how the run is going, for the line beside a step that waits on
// it: how it ended once it is over, and what it is doing until then.
func (w *WorkflowRun) State() string {
	if w.Finished() {
		return w.Conclusion
	}

	return strings.ReplaceAll(w.Status, "_", " ")
}

// Commit is what a person reads about a commit before building on it.
type Commit struct {
	SHA     string
	Subject string
	// Date is when the commit was made, as GitHub writes it.
	Date string
}

// repoFacts is what the release detection asks about the release repository.
// It is an interface so the tests can answer with output captured from real
// runs.
type repoFacts interface {
	// Refs lists the release branches and release tags with their commits.
	Refs(ctx context.Context) (*Refs, error)
	// ReleaseState reports whether a release names the tag, and whether it
	// is still a draft.
	ReleaseState(ctx context.Context, tag string) (ReleaseState, error)
	// TagInMain reports whether the tag's commit has reached the default
	// branch, which is what marks a release done.
	TagInMain(ctx context.Context, tag string) (bool, error)
}

// repository reaches the profile's repository through git and gh, so both
// keep their own credentials.
type repository struct {
	repo string
	// url is the remote to fetch and read refs from. Remotes are found by
	// URL, never by name, so a clone that calls the release repository
	// anything at all still works.
	url string
	// pushURL is the same remote's push URL. A clone can set it apart from
	// url, to push over ssh or to refuse pushes with DISABLED, and every push
	// goes to it.
	pushURL string
	// dir is the clone this repository is checked out in, which is where its
	// remotes are read. Empty is the clone the tool runs in.
	dir string
	run commander
	// refs is read once and shared by every step that checks a branch or a
	// tag, so one refresh makes one call.
	refs *Refs
	// fork is the repository a step pushes its branches to, which is where a
	// release's work is until somebody merges it. It is looked up once,
	// because finding it costs two API calls.
	fork *repository
	// mainHead is read once, so an action that branches from main pushes the
	// commit its confirmation showed.
	mainHead *Commit
	// root is the top of the clone the tool runs in.
	root string
	// releases caches what GitHub knows about a tag's release, because the
	// detection, the draft step and the notes step all ask about it. A tag
	// with no release is cached as nil.
	releases map[string]*releaseView
	// runs caches the workflow runs by ref, because a step's check and its
	// precondition ask about the same one.
	runs map[string]*WorkflowRun
	// artifacts caches what a run built, because every step that uploads an
	// asset asks the same run about its own file.
	artifacts map[string][]Artifact
}

func newRepository(ctx context.Context, clone, repo string, run commander) (*repository, error) {
	fetch, push, err := remoteURLs(ctx, clone, repo, run)
	if err != nil {
		return nil, err
	}

	return &repository{repo: repo, url: fetch, pushURL: push, dir: clone, run: run}, nil
}

// remoteURLs are the fetch and push URLs of a clone's remote for the
// repository, or the repository's public URL for both when no remote fetches
// from it. A repository is read in the clone it is checked out in, because a
// URL the user has already pushed to needs no credentials set up a second
// time.
func remoteURLs(ctx context.Context, clone, repo string, run commander) (string, string, error) {
	output, err := run.runIn(ctx, clone, "git", "remote", "--verbose")
	if err != nil {
		return "", "", fmt.Errorf("reading the remotes for %s: %w", repo, err)
	}

	type remote struct{ fetch, push string }

	var names []string

	remotes := map[string]*remote{}

	// A line reads "<name>\t<url> (fetch)", followed by the filter in a
	// partial clone, or "<name>\t<url> (push)".
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		name, rest, _ := strings.Cut(line, "\t")

		found, seen := remotes[name]
		if !seen {
			found = &remote{}
			remotes[name] = found
			names = append(names, name)
		}

		if url, _, fetch := strings.Cut(rest, " (fetch)"); fetch {
			found.fetch = url
		} else if url, _, push := strings.Cut(rest, " (push)"); push {
			found.push = url
		}
	}

	for _, name := range names {
		if found := remotes[name]; sameRepo(found.fetch, repo) {
			return found.fetch, found.push, nil
		}
	}

	public := "https://github.com/" + repo + ".git"

	return public, public, nil
}

// sameRepo reports whether a git remote URL points at the owner/name
// repository.
func sameRepo(url, repo string) bool {
	return strings.EqualFold(repoPath(url), repo)
}

// repoPath is the owner/name a git remote URL names, in any of the spellings a
// clone may hold: an https or ssh URL, an scp-style address, with or without
// the .git suffix and a trailing slash. Anything else, a local path among them,
// keeps what it has and so names no repository.
func repoPath(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")

	// The first segment of a URL's path is the host, which may carry a port.
	if _, rest, found := strings.Cut(trimmed, "://"); found {
		_, after, _ := strings.Cut(rest, "/")

		return after
	}

	// An scp-style address puts the path after the colon. A local path can
	// hold a colon too, but then a slash comes before it.
	if host, after, found := strings.Cut(trimmed, ":"); found && !strings.Contains(host, "/") {
		return after
	}

	return trimmed
}

func (r *repository) Refs(ctx context.Context) (*Refs, error) {
	if r.refs != nil {
		return r.refs, nil
	}

	output, err := r.run.run(ctx, "git", "ls-remote", r.url, "refs/heads/release-*", "refs/tags/v*")
	if err != nil {
		return nil, fmt.Errorf("listing the refs of %s: %w", r.repo, err)
	}

	r.refs = ParseRefs(string(output))

	return r.refs, nil
}

// releaseFields are the parts of a release the tool reads.
const releaseFields = "isDraft,body,assets"

// releaseView is what GitHub answers about the release for a tag.
type releaseView struct {
	IsDraft bool           `json:"isDraft"`
	Body    string         `json:"body"`
	Assets  []releaseAsset `json:"assets"`
}

// releaseAsset is a file attached to a release.
type releaseAsset struct {
	Name string `json:"name"`
	// Digest is the sha256 GitHub made of the bytes it stored, written
	// "sha256:<hex>".
	Digest string `json:"digest"`
	// State is "uploaded" once the whole file has arrived. An upload that
	// stopped partway leaves the name behind in another state, so a name
	// alone does not mean the file is there.
	State string `json:"state"`
}

// uploaded is the state of an asset GitHub has the whole of.
const uploaded = "uploaded"

// releaseFor reads the release for a tag, or errNotFound when no release
// names it.
func (r *repository) releaseFor(ctx context.Context, tag string) (*releaseView, error) {
	view, cached := r.releases[tag]
	if !cached {
		var err error

		view, err = r.readRelease(ctx, tag)
		if err != nil && !errors.Is(err, errNotFound) {
			return nil, err
		}

		if r.releases == nil {
			r.releases = map[string]*releaseView{}
		}

		r.releases[tag] = view
	}

	if view == nil {
		return nil, fmt.Errorf("reading the release %s: %w", tag, errNotFound)
	}

	return view, nil
}

func (r *repository) readRelease(ctx context.Context, tag string) (*releaseView, error) {
	output, err := r.run.run(ctx, "gh", "release", "view", tag, "--repo", r.repo, "--json", releaseFields)
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && (failure.says("release not found") || failure.missing()) {
			return nil, errNotFound
		}

		return nil, fmt.Errorf("reading the release %s: %w", tag, err)
	}

	view := &releaseView{}

	if err := json.Unmarshal(output, view); err != nil {
		return nil, fmt.Errorf("reading the release %s: %w", tag, err)
	}

	return view, nil
}

func (r *repository) ReleaseState(ctx context.Context, tag string) (ReleaseState, error) {
	view, err := r.releaseFor(ctx, tag)

	switch {
	case errors.Is(err, errNotFound):
		return ReleaseMissing, nil
	case err != nil:
		return "", err
	case view.IsDraft:
		return ReleaseDraft, nil
	default:
		return ReleasePublished, nil
	}
}

// ReleaseNotes is the body of the release for a tag, what a reader sees under
// the release on GitHub.
func (r *repository) ReleaseNotes(ctx context.Context, tag string) (string, error) {
	view, err := r.releaseFor(ctx, tag)
	if err != nil {
		return "", err
	}

	return view.Body, nil
}

// ReleaseAssets are the files attached to the release for a tag.
func (r *repository) ReleaseAssets(ctx context.Context, tag string) ([]releaseAsset, error) {
	view, err := r.releaseFor(ctx, tag)
	if err != nil {
		return nil, err
	}

	return view.Assets, nil
}

// ReleaseAssetsNow are the files attached to a release, read again. The
// cache holds the release as the refresh found it, which is out of date once
// an action has uploaded to it or spent minutes downloading.
func (r *repository) ReleaseAssetsNow(ctx context.Context, tag string) ([]releaseAsset, error) {
	delete(r.releases, tag)

	return r.ReleaseAssets(ctx, tag)
}

// Root is the top of the clone the tool runs in, which is where the working
// copy of the release notes is.
func (r *repository) Root(ctx context.Context) (string, error) {
	if r.root != "" {
		return r.root, nil
	}

	output, err := r.run.runIn(ctx, r.dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("finding the top of the clone: %w", err)
	}

	r.root = strings.TrimSpace(string(output))

	return r.root, nil
}

// FileAtRef is a file's content at a branch, tag or commit, read through the
// API so a check gives the same answer whatever the local clone has fetched.
func (r *repository) FileAtRef(ctx context.Context, ref, path string) ([]byte, error) {
	return fileAtRef(ctx, r.run, r.repo, ref, path)
}

// fileAtRef reads a file from any repository. The guest image pins need that,
// because they are in repositories no profile names.
func fileAtRef(ctx context.Context, run commander, repo, ref, path string) ([]byte, error) {
	query := fmt.Sprintf("repos/%s/contents/%s?ref=%s", repo, path, ref)

	output, err := run.run(ctx, "gh", "api", query, "--header", "Accept: application/vnd.github.raw")
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && failure.missing() {
			err = errNotFound
		}

		return nil, fmt.Errorf("reading %s at %s of %s: %w", path, ref, repo, err)
	}

	return output, nil
}

// FilesAtRef are the names of the files in a directory at a ref, which is
// what a step needs to leave a directory holding the newest few of something.
func (r *repository) FilesAtRef(ctx context.Context, ref, dir string) ([]string, error) {
	query := fmt.Sprintf("repos/%s/contents/%s?ref=%s", r.repo, dir, ref)

	output, err := r.run.run(ctx, "gh", "api", query, "--jq", ".[].name")
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && failure.missing() {
			err = errNotFound
		}

		return nil, fmt.Errorf("listing %s at %s of %s: %w", dir, ref, r.repo, err)
	}

	var names []string

	for name := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if name != "" {
			names = append(names, name)
		}
	}

	return names, nil
}

// BranchHead is the commit a branch points at, or errNotFound when the
// repository has no such branch. It asks about the branch, not the commit.
// The commits endpoint resolves a ref as a SHA and answers an unknown name
// with 422, which missing() does not count as an absence.
func (r *repository) BranchHead(ctx context.Context, branch string) (string, error) {
	output, err := r.run.run(ctx, "gh", "api", "repos/"+r.repo+"/branches/"+branch, "--jq", ".commit.sha")
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && failure.missing() {
			err = errNotFound
		}

		return "", fmt.Errorf("reading the head of %s in %s: %w", branch, r.repo, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// MainHead is the commit at the head of the default branch, which a new
// release line starts from.
func (r *repository) MainHead(ctx context.Context) (*Commit, error) {
	if r.mainHead != nil {
		return r.mainHead, nil
	}

	output, err := r.run.run(ctx, "gh", "api", "repos/"+r.repo+"/branches/"+defaultBranch)
	if err != nil {
		return nil, fmt.Errorf("reading the head of %s in %s: %w", defaultBranch, r.repo, err)
	}

	var answer struct {
		Commit struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message   string `json:"message"`
				Committer struct {
					Date string `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		} `json:"commit"`
	}

	if err := json.Unmarshal(output, &answer); err != nil {
		return nil, fmt.Errorf("reading the head of %s in %s: %w", defaultBranch, r.repo, err)
	}

	head := answer.Commit
	subject, _, _ := strings.Cut(head.Commit.Message, "\n")
	r.mainHead = &Commit{SHA: head.SHA, Subject: subject, Date: head.Commit.Committer.Date}

	return r.mainHead, nil
}

// Fork is the repository the user's own branches go to, which a check reads
// to find work that is pushed but not yet merged. A user with no fork gets
// the repository itself, which is what a profile that already points at a
// fork needs.
func (r *repository) Fork(ctx context.Context) (*repository, error) {
	if r.fork != nil {
		return r.fork, nil
	}

	output, err := r.run.run(ctx, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return nil, fmt.Errorf("reading who you are signed in as: %w", err)
	}

	login := strings.TrimSpace(string(output))
	_, name, _ := strings.Cut(r.repo, "/")
	fork := login + "/" + name

	// Only a missing repository means there is no fork. Any other failure
	// says nothing about it, and guessing would send a branch to the release
	// repository itself.
	parent, err := r.run.run(ctx, "gh", "api", "repos/"+fork, "--jq", ".parent.full_name")

	var failure *commandFailure

	switch {
	case err == nil && strings.EqualFold(strings.TrimSpace(string(parent)), r.repo):
		r.fork, err = newRepository(ctx, r.dir, fork, r.run)
		if err != nil {
			return nil, err
		}
	case err == nil, errors.As(err, &failure) && failure.missing():
		r.fork = r
	default:
		return nil, fmt.Errorf("looking for your fork of %s: %w", r.repo, err)
	}

	return r.fork, nil
}

// OpenPR is the number of the open pull request from a branch, or zero when
// there is none.
func (r *repository) OpenPR(ctx context.Context, owner, branch string) (int, error) {
	query := fmt.Sprintf("repos/%s/pulls?state=open&head=%s:%s", r.repo, owner, branch)

	output, err := r.run.run(ctx, "gh", "api", query, "--jq", ".[0].number")
	if err != nil {
		return 0, fmt.Errorf("looking for a pull request from %s:%s: %w", owner, branch, err)
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		return 0, nil
	}

	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("looking for a pull request from %s:%s: %w", owner, branch, err)
	}

	return number, nil
}

// PushTarget is the owner of the repository a step's own branch goes to, and
// the URL the branch is pushed to.
func (r *repository) PushTarget(ctx context.Context) (string, string, error) {
	fork, err := r.Fork(ctx)
	if err != nil {
		return "", "", err
	}

	owner, _, _ := strings.Cut(fork.repo, "/")

	return owner, fork.pushURL, nil
}

func (r *repository) TagInMain(ctx context.Context, tag string) (bool, error) {
	return r.InBranch(ctx, tag, defaultBranch)
}

// count asks the API a question a number answers, and wraps a failure in
// what was being read.
func (r *repository) count(ctx context.Context, query, filter, reading string) (int, error) {
	output, err := r.run.run(ctx, "gh", "api", query, "--jq", filter)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", reading, err)
	}

	number, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", reading, err)
	}

	return number, nil
}

// InBranch reports whether a branch already has a commit or tag.
func (r *repository) InBranch(ctx context.Context, ref, branch string) (bool, error) {
	// behind_by counts the commits the ref has that the branch does not, so
	// zero means the branch already has it.
	behind, err := r.count(ctx,
		fmt.Sprintf("repos/%s/compare/%s...%s", r.repo, ref, branch), ".behind_by",
		fmt.Sprintf("comparing %s with %s", ref, branch))
	if err != nil {
		return false, err
	}

	return behind == 0, nil
}

// LatestRun is the newest run of a workflow for a ref, or nil when the
// workflow has not run for it. GitHub starts a run per ref, so the release
// branch head and the tag on that same commit each have one of their own,
// and the commit alone would not tell them apart.
func (r *repository) LatestRun(ctx context.Context, workflow, ref, commit string) (*WorkflowRun, error) {
	key := workflow + " " + ref + " " + commit
	if cached, ok := r.runs[key]; ok {
		return cached, nil
	}

	query := fmt.Sprintf("repos/%s/actions/workflows/%s/runs?branch=%s&head_sha=%s&per_page=1",
		r.repo, workflow, ref, commit)

	output, err := r.run.run(ctx, "gh", "api", query)
	if err != nil {
		return nil, fmt.Errorf("reading the %s runs for %s: %w", workflow, ref, err)
	}

	var answer struct {
		Runs []struct {
			// The id is an identifier rather than a quantity, and gh takes
			// it as one more argument, so it never becomes a number here.
			ID         json.Number `json:"id"`
			Status     string      `json:"status"`
			Conclusion string      `json:"conclusion"`
			URL        string      `json:"html_url"`
		} `json:"workflow_runs"`
	}

	if err := json.Unmarshal(output, &answer); err != nil {
		return nil, fmt.Errorf("reading the %s runs for %s: %w", workflow, ref, err)
	}

	var latest *WorkflowRun

	if len(answer.Runs) > 0 {
		newest := answer.Runs[0]
		latest = &WorkflowRun{
			ID:         newest.ID.String(),
			Status:     newest.Status,
			Conclusion: newest.Conclusion,
			URL:        newest.URL,
		}
	}

	if r.runs == nil {
		r.runs = map[string]*WorkflowRun{}
	}

	r.runs[key] = latest

	return latest, nil
}

// CheckStates are how the checks on a commit are going, one entry per check,
// in the words a workflow run's State uses. gh prints the conclusion of a
// check still running as null, which State never reads.
func (r *repository) CheckStates(ctx context.Context, commit string) ([]string, error) {
	output, err := r.run.run(ctx, "gh", "api", "--paginate",
		fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", r.repo, commit),
		"--jq", `.check_runs[] | "\(.status) \(.conclusion)"`)
	if err != nil {
		return nil, fmt.Errorf("reading the checks on %.7s: %w", commit, err)
	}

	var states []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}

		status, conclusion, _ := strings.Cut(line, " ")
		states = append(states, (&WorkflowRun{Status: status, Conclusion: conclusion}).State())
	}

	return states, nil
}

// Artifact is a file a workflow run uploaded, which is where a release asset
// comes from.
type Artifact struct {
	Name string
	// Expired is true once GitHub has removed the file. The entry outlives
	// the bytes, so a name alone does not mean the file can be downloaded.
	Expired bool
	// Size is the artifact's size in bytes, which a step shows before it
	// starts a download.
	Size int64
}

// Artifacts are the files a workflow run uploaded.
func (r *repository) Artifacts(ctx context.Context, runID string) ([]Artifact, error) {
	if cached, ok := r.artifacts[runID]; ok {
		return cached, nil
	}

	query := fmt.Sprintf("repos/%s/actions/runs/%s/artifacts?per_page=100", r.repo, runID)

	output, err := r.run.run(ctx, "gh", "api", query)
	if err != nil {
		return nil, fmt.Errorf("reading what the run %s built: %w", runID, err)
	}

	var answer struct {
		Total     int `json:"total_count"`
		Artifacts []struct {
			Name    string `json:"name"`
			Expired bool   `json:"expired"`
			Size    int64  `json:"size_in_bytes"`
		} `json:"artifacts"`
	}

	if err := json.Unmarshal(output, &answer); err != nil {
		return nil, fmt.Errorf("reading what the run %s built: %w", runID, err)
	}

	// The listing is one page, so a run with more artifacts than that has
	// pages nobody read. Failing beats reporting a file a later page holds
	// as one the run never built.
	if answer.Total > len(answer.Artifacts) {
		return nil, fmt.Errorf("the run %s built %d files, more than one page holds", runID, answer.Total)
	}

	built := make([]Artifact, 0, len(answer.Artifacts))
	for _, artifact := range answer.Artifacts {
		built = append(built, Artifact{Name: artifact.Name, Expired: artifact.Expired, Size: artifact.Size})
	}

	if r.artifacts == nil {
		r.artifacts = map[string][]Artifact{}
	}

	r.artifacts[runID] = built

	return built, nil
}

// GeneratedNotes is GitHub's own draft of the notes for a tag: the pull
// requests merged since the previous release, and the authors it counts as
// contributing for the first time. Drafting creates nothing.
func (r *repository) GeneratedNotes(ctx context.Context, tag, previous, target string) (string, error) {
	output, err := r.run.run(ctx, "gh", "api", "--method", "POST",
		"repos/"+r.repo+"/releases/generate-notes",
		"--field", "tag_name="+tag,
		"--field", "previous_tag_name="+previous,
		"--field", "target_commitish="+target,
		"--jq", ".body")
	if err != nil {
		return "", fmt.Errorf("asking GitHub to draft the notes for %s: %w", tag, err)
	}

	return string(output), nil
}

// CommitDate is when a ref's commit was made, in the form the API takes as a
// bound on a search of the history.
func (r *repository) CommitDate(ctx context.Context, ref string) (string, error) {
	output, err := r.run.run(ctx, "gh", "api", "repos/"+r.repo+"/commits/"+ref,
		"--jq", ".commit.committer.date")
	if err != nil {
		return "", fmt.Errorf("reading the date of %s: %w", ref, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// CommittedBefore reports whether an author has a commit in the repository
// as old as a date.
func (r *repository) CommittedBefore(ctx context.Context, author, date string) (bool, error) {
	commits, err := r.count(ctx,
		fmt.Sprintf("repos/%s/commits?author=%s&until=%s&per_page=1", r.repo, author, date),
		"length", fmt.Sprintf("reading what %s committed before %s", author, date))
	if err != nil {
		return false, err
	}

	return commits > 0, nil
}

// Milestone is the number of the milestone with a title, or zero when the
// repository has none. Milestones are listed rather than searched, because
// the API offers no lookup by title.
func (r *repository) Milestone(ctx context.Context, title string) (int, error) {
	output, err := r.run.run(ctx, "gh", "api", "--paginate",
		"repos/"+r.repo+"/milestones?state=all&per_page=100",
		"--jq", fmt.Sprintf(".[] | select(.title == %q) | .number", title))
	if err != nil {
		return 0, fmt.Errorf("looking for the milestone %q: %w", title, err)
	}

	found := strings.TrimSpace(string(output))
	if found == "" {
		return 0, nil
	}

	// A repository can have the same title twice; the first is the answer.
	number, err := strconv.Atoi(strings.Fields(found)[0])
	if err != nil {
		return 0, fmt.Errorf("looking for the milestone %q: %w", title, err)
	}

	return number, nil
}
