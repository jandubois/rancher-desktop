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
// there, which is an answer a check reads rather than a failure.
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
	// url is the remote to read refs from. Remotes are found by URL, never
	// by name, so a clone that calls the release repository anything at all
	// still works.
	url string
	run commander
	// refs is read once and shared by every step that checks a branch or a
	// tag, so one refresh makes one call.
	refs *Refs
	// pushOwner and pushURL are where the user's own branches go, looked up
	// once because finding them costs two API calls.
	pushOwner string
	pushURL   string
	// root is the top of the clone the tool runs in.
	root string
	// releases caches what GitHub knows about a tag's release, because the
	// detection, the draft step and the notes step all ask about it. A tag
	// with no release is cached as nil.
	releases map[string]*releaseView
	// runs caches the workflow runs by ref, because a step's check and its
	// precondition ask about the same one.
	runs map[string]*WorkflowRun
}

func newRepository(ctx context.Context, repo string, run commander) *repository {
	return &repository{repo: repo, url: remoteURL(ctx, repo, run), run: run}
}

// remoteURL is the URL of the clone's remote for the repository, or the
// repository's public URL when no remote points at it.
func remoteURL(ctx context.Context, repo string, run commander) string {
	output, err := run.run(ctx, "git", "remote", "--verbose")
	if err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			if sameRepo(fields[1], repo) {
				return fields[1]
			}
		}
	}

	return "https://github.com/" + repo + ".git"
}

// sameRepo reports whether a git remote URL points at the owner/name
// repository, in any of the spellings a clone may hold.
func sameRepo(url, repo string) bool {
	trimmed := strings.TrimSuffix(url, ".git")
	if host, path, found := strings.Cut(trimmed, ":"); found && !strings.Contains(host, "/") {
		trimmed = path
	}

	return strings.EqualFold(strings.TrimPrefix(trimmed, "https://github.com/"), repo)
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

// releaseView is what GitHub answers about the release for a tag.
type releaseView struct {
	IsDraft bool   `json:"isDraft"`
	Body    string `json:"body"`
}

// releaseFor reads the release for a tag, or errNotFound when no release
// names it.
func (r *repository) releaseFor(ctx context.Context, tag string) (*releaseView, error) {
	if view, cached := r.releases[tag]; cached {
		if view == nil {
			return nil, errNotFound
		}

		return view, nil
	}

	view, err := r.readRelease(ctx, tag)
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, err
	}

	if r.releases == nil {
		r.releases = map[string]*releaseView{}
	}

	r.releases[tag] = view

	return view, err
}

func (r *repository) readRelease(ctx context.Context, tag string) (*releaseView, error) {
	output, err := r.run.run(ctx, "gh", "release", "view", tag, "--repo", r.repo, "--json", "isDraft,body")
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

// Root is the top of the clone the tool runs in, which is where the working
// copy of the release notes is.
func (r *repository) Root(ctx context.Context) (string, error) {
	if r.root != "" {
		return r.root, nil
	}

	output, err := r.run.run(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("finding the top of the clone: %w", err)
	}

	r.root = strings.TrimSpace(string(output))

	return r.root, nil
}

// FileAtRef is a file's content at a branch, tag or commit, read through the
// API so a check gives the same answer whatever the local clone has fetched.
func (r *repository) FileAtRef(ctx context.Context, ref, path string) ([]byte, error) {
	query := fmt.Sprintf("repos/%s/contents/%s?ref=%s", r.repo, path, ref)

	output, err := r.run.run(ctx, "gh", "api", query, "--header", "Accept: application/vnd.github.raw")
	if err != nil {
		var failure *commandFailure
		if errors.As(err, &failure) && failure.missing() {
			return nil, errNotFound
		}

		return nil, fmt.Errorf("reading %s at %s: %w", path, ref, err)
	}

	return output, nil
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

// PushTarget is where a step's own branch goes: the user's fork of the
// release repository, or the release repository itself when they have no
// fork of it, which is what a profile pointing at a fork already wants.
func (r *repository) PushTarget(ctx context.Context) (string, string, error) {
	if r.pushOwner != "" {
		return r.pushOwner, r.pushURL, nil
	}

	output, err := r.run.run(ctx, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return "", "", fmt.Errorf("reading who you are signed in as: %w", err)
	}

	login := strings.TrimSpace(string(output))
	_, name, _ := strings.Cut(r.repo, "/")
	fork := login + "/" + name

	parent, err := r.run.run(ctx, "gh", "api", "repos/"+fork, "--jq", ".parent.full_name")
	if err == nil && strings.TrimSpace(string(parent)) == r.repo {
		r.pushOwner, r.pushURL = login, remoteURL(ctx, fork, r.run)

		return r.pushOwner, r.pushURL, nil
	}

	r.pushOwner, _, _ = strings.Cut(r.repo, "/")
	r.pushURL = r.url

	return r.pushOwner, r.pushURL, nil
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
