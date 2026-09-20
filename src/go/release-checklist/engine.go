// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// State is what the checklist shows for one step.
type State string

const (
	// Done means the check found the step's work already in place.
	Done State = "done"
	// Available means the step can be run now.
	Available State = "available"
	// Waiting means a process outside the tool has to finish first.
	Waiting State = "waiting"
	// Blocked means a precondition failed. The detail says how to fix it.
	Blocked State = "blocked"
	// Skipped means the step does not apply to this release, or the profile
	// leaves out the system it touches.
	Skipped State = "skipped"
	// Unknown means a check could not decide: nothing answered, or the
	// answer could not be read.
	Unknown State = "unknown"
)

// Status is a step's state and the one line shown beside it.
type Status struct {
	State  State
	Detail string
}

// Answer is what a check or a precondition reports.
type Answer struct {
	// OK is true when the check found the work done, or when every
	// precondition holds.
	OK bool
	// Detail is the line to show, whether OK or not.
	Detail string
	// Waiting marks a precondition that nobody can satisfy by hand because
	// a process outside the tool is still running.
	Waiting bool
}

// Step is one entry in the release checklist. Every step carries the same
// parts, so the engine treats them all alike.
type Step struct {
	// ID is the step's number in the checklist, such as "1" or "20a".
	ID string
	// Title names the step.
	Title string
	// Kinds are the release kinds the step applies to. Every step names its
	// own, so a step that names none runs for no release at all.
	Kinds []Kind
	// Needs are the systems outside this machine that the step reads or
	// writes. The engine probes each one before the check runs.
	Needs []*Resource
	// Doc is the prose for the detail pane and the README.
	Doc Doc
	// Check reports whether the step's work is already in place. It reads
	// the source of truth on every refresh and trusts no stored flag, so it
	// notices work done by hand or out of order.
	Check func(context.Context, *Run) (Answer, error)
	// Precondition reports whether the step can run now. A nil precondition
	// means the resources in Needs are all it takes.
	Precondition func(context.Context, *Run) (Answer, error)
	// Confirms is the text somebody marks done for this step, read from the
	// system the step checks. A step with one is done only once a person has
	// marked it, and a change to that text puts it back to available, so the
	// person marks what they have read. A step without one takes no
	// judgment, and its check settles it.
	Confirms func(context.Context, *Run) (string, error)
	// Action is the step's automation. A step without one is done by hand,
	// following its instructions.
	Action *Action
	// Facts is the reference material the step's manual work is done from,
	// read from the release itself. A step without one needs only what its
	// instructions already say.
	Facts *Facts
}

// Facts is a step's reference material. It runs to more than a pane holds,
// and the reader pastes from it into a draft, so it goes to a file.
type Facts struct {
	// Title says what the material is, for the README and the dashboard.
	Title string
	// File is the name the material is written under, in the release's
	// cache directory.
	File string
	// Gather builds the material. It reads the release and changes nothing.
	Gather func(context.Context, *Run) (string, error)
}

// Action is a step's automation. It is a list of operations rather than a
// function body, so the confirmation the user reads, the README's account of
// the step, and what actually runs all come from one place.
type Action struct {
	// Title says what running the action does.
	Title string
	// Summary shows what the action would change, above the operations and
	// the question. An action whose operations speak for themselves has
	// none.
	Summary func(context.Context, *Run) (string, error)
	// Plan builds the operations for this release. It runs before the user
	// confirms anything, so it must change nothing itself.
	Plan func(context.Context, *Run) ([]Operation, error)
}

// Operation is one piece of an action.
type Operation struct {
	// Description is the line shown for confirmation and in the README. For
	// an external command it is the command line.
	Description string
	// Command, Args and Dir are the external command to run. An operation
	// with no command does its work in Do.
	Command string
	Args    []string
	Dir     string
	// Do is work that no single command expresses, such as replacing the
	// version in package.json.
	Do func(context.Context, *Run) error
}

// Doc is a step's prose. The placeholders {version}, {tag}, {branch}, {line},
// {repo} and {docsRepo} stand for the release's own values, which the detail
// pane fills in and the README keeps, so the README describes every release.
type Doc struct {
	// Applies says which releases the step is for.
	Applies string
	// Check says what makes the step done.
	Check string
	// Precondition says what the step waits for.
	Precondition string
	// Instructions say how to do the step by hand. They are complete on
	// their own, so a step with no automation is still a step.
	Instructions string
}

// Resource is a system outside this machine that the steps reach through a
// command line tool. The engine probes each resource once per refresh and
// shares the answer between the steps that need it, and the tool keeps no
// credentials of its own: each tool uses its own store.
type Resource struct {
	// Name identifies the resource in the checklist.
	Name string
	// Command is the executable the engine probes the resource with, and
	// the one a step names when it is missing.
	Command string
	// Install is where to get the command.
	Install string
	// Configured reports whether the profile names this resource. A profile
	// that leaves a section out turns its steps off.
	Configured func(*Profile) bool
	// Probe reports whether the profile's resource answers with the access
	// the steps need.
	Probe func(context.Context, *Run) (Answer, error)
}

// Run is one refresh of the checklist: the release being driven, the profile
// naming its resources, and the probe answers shared between the steps.
type Run struct {
	Release *Release
	Profile *Profile
	Repo    *repository
	Refs    *Refs
	Tools   commander
	// Confirmations are the steps of this profile somebody has marked done.
	Confirmations confirmations
	// Settings are the paths on this machine a step works in.
	Settings Settings

	probed     map[string]Status
	statuses   map[string]Status
	evaluating map[string]bool
	docs       *repository
}

// Docs is the documentation repository, built when a step first asks for it
// because only the documentation steps reach it. Its remotes are the ones the
// documentation clone holds, so the URLs a step pushes to are the ones the user
// already pushes to by hand.
func (r *Run) Docs(ctx context.Context) *repository {
	if r.docs == nil {
		r.docs = newRepository(ctx, r.Settings.DocsClone, r.Profile.GitHub.DocsRepo, r.Tools)
	}

	return r.docs
}

// newRun starts a refresh.
func newRun(release *Release, profile *Profile, repo *repository) *Run {
	return &Run{
		Release:    release,
		Profile:    profile,
		Repo:       repo,
		Tools:      repo.run,
		probed:     map[string]Status{},
		statuses:   map[string]Status{},
		evaluating: map[string]bool{},
	}
}

// Status works out what to show for one step and remembers it for the rest of
// the refresh, so a step that waits on another reads its state without
// checking it a second time.
func (r *Run) Status(ctx context.Context, step *Step) Status {
	if status, ok := r.statuses[step.ID]; ok {
		return status
	}

	if r.evaluating[step.ID] {
		return Status{State: Unknown, Detail: "step " + step.ID + " waits on itself"}
	}

	if r.statuses == nil {
		r.statuses, r.evaluating = map[string]Status{}, map[string]bool{}
	}

	r.evaluating[step.ID] = true
	status := Evaluate(ctx, step, r)
	delete(r.evaluating, step.ID)

	r.statuses[step.ID] = status

	return status
}

// waitFor holds a step until the steps it follows are done, or skipped
// because this release does not run them.
func waitFor(ctx context.Context, run *Run, needed ...*Step) Answer {
	for _, step := range needed {
		switch status := run.Status(ctx, step); status.State {
		case Done, Skipped:
		case Waiting:
			return Answer{
				Waiting: true,
				Detail:  fmt.Sprintf("step %s, %s, is %s", step.ID, step.Title, status.Detail),
			}
		default:
			return Answer{Detail: fmt.Sprintf("step %s, %s, is %s", step.ID, step.Title, status.State)}
		}
	}

	return Answer{OK: true}
}

// Evaluate works out what to show for one step. It asks the same questions in
// the same order for every step, so no step is special-cased.
func Evaluate(ctx context.Context, step *Step, run *Run) Status {
	if state, ok := step.skip(run); ok {
		return state
	}

	for _, resource := range step.Needs {
		if status := run.probe(ctx, resource); status.State != Done {
			return status
		}
	}

	answer, err := step.Check(ctx, run)
	if err != nil {
		return Status{State: Unknown, Detail: err.Error()}
	}

	if answer.OK {
		if step.Confirms == nil {
			return Status{State: Done, Detail: answer.Detail}
		}

		return judged(ctx, step, run, answer)
	}

	if step.Precondition == nil {
		return Status{State: Available, Detail: answer.Detail}
	}

	ready, err := step.Precondition(ctx, run)

	switch {
	case err != nil:
		return Status{State: Unknown, Detail: err.Error()}
	case ready.OK:
		return Status{State: Available, Detail: answer.Detail}
	case ready.Waiting:
		return Status{State: Waiting, Detail: ready.Detail}
	default:
		return Status{State: Blocked, Detail: ready.Detail}
	}
}

// judged is the status of a step whose check has passed but whose work takes
// judgment. The step is done once a person has marked the text the check
// read, and available again when that text changes.
func judged(ctx context.Context, step *Step, run *Run, answer Answer) Status {
	subject, err := step.Confirms(ctx, run)
	if err != nil {
		return Status{State: Unknown, Detail: err.Error()}
	}

	confirmation, marked := run.Confirmation(step.ID)

	switch {
	case !marked:
		return Status{State: Available, Detail: answer.Detail + "; nobody has marked it done"}
	case confirmation.Digest != digestOf(subject):
		return Status{State: Available, Detail: fmt.Sprintf(
			"%s; what you marked done on %s has changed",
			answer.Detail, markedOn(confirmation))}
	default:
		return Status{State: Done, Detail: fmt.Sprintf(
			"%s; marked done on %s", answer.Detail, markedOn(confirmation))}
	}
}

// markedOn is the day somebody marked a step done, in their own time zone.
func markedOn(confirmation Confirmation) string {
	return confirmation.At.Local().Format(time.DateOnly)
}

// Confirmation is what somebody has marked for a step of this release. A run
// built without a store has marked nothing.
func (r *Run) Confirmation(step string) (Confirmation, bool) {
	if r.Confirmations == nil {
		return Confirmation{}, false
	}

	return r.Confirmations.Confirmed(r.Release.Version, step)
}

// Mark records that a person has judged a step done, or takes the mark off
// when they mark it a second time. It reads the text at the moment of
// marking, so the mark covers what they have in front of them.
func Mark(ctx context.Context, step *Step, run *Run) error {
	if step.Confirms == nil {
		return fmt.Errorf("step %s is settled by its check, so there is nothing to mark", step.ID)
	}

	if run.Confirmations == nil {
		return errors.New("this checklist has nowhere to keep confirmations")
	}

	subject, err := step.Confirms(ctx, run)
	if err != nil {
		return err
	}

	if subject == "" {
		return fmt.Errorf("step %s has nothing to mark done yet", step.ID)
	}

	digest := digestOf(subject)

	if confirmation, marked := run.Confirmation(step.ID); marked && confirmation.Digest == digest {
		return run.Confirmations.Unconfirm(run.Release.Version, step.ID)
	}

	return run.Confirmations.Confirm(run.Release.Version, step.ID, digest)
}

// skip reports the steps this release never runs: the ones for the other kind
// of release, and the ones whose system the profile leaves out.
func (s *Step) skip(run *Run) (Status, bool) {
	if !slices.Contains(s.Kinds, run.Release.Kind) {
		return Status{State: Skipped, Detail: "for " + kinds(s.Kinds)}, true
	}

	for _, resource := range s.Needs {
		if resource.Configured != nil && !resource.Configured(run.Profile) {
			return Status{
				State:  Skipped,
				Detail: fmt.Sprintf("the %s profile names no %s", run.Profile.Name, resource.Name),
			}, true
		}
	}

	return Status{}, false
}

// probe answers whether a resource is reachable with the access the steps
// need. A resource is probed once per refresh, however many steps need it.
func (r *Run) probe(ctx context.Context, resource *Resource) Status {
	if status, ok := r.probed[resource.Name]; ok {
		return status
	}

	status := r.runProbe(ctx, resource)

	if r.probed == nil {
		r.probed = map[string]Status{}
	}

	r.probed[resource.Name] = status

	return status
}

func (r *Run) runProbe(ctx context.Context, resource *Resource) Status {
	if !r.Tools.installed(resource.Command) {
		return Status{
			State:  Blocked,
			Detail: fmt.Sprintf("%s is not installed (%s)", resource.Command, resource.Install),
		}
	}

	answer, err := resource.Probe(ctx, r)

	switch {
	case err != nil:
		return Status{State: Unknown, Detail: err.Error()}
	case !answer.OK:
		return Status{State: Blocked, Detail: answer.Detail}
	default:
		return Status{State: Done, Detail: answer.Detail}
	}
}

// kinds names the release kinds a step applies to, for the line beside it.
func kinds(applies []Kind) string {
	named := make([]string, 0, len(applies))
	for _, kind := range applies {
		named = append(named, string(kind)+" releases")
	}

	return strings.Join(named, " and ")
}

// fill replaces a step's placeholders with the release's own values.
func fill(text string, run *Run) string {
	return strings.NewReplacer(
		"{version}", run.Release.Version.String(),
		"{tag}", run.Release.Tag(),
		"{branch}", run.Release.Branch(),
		"{line}", run.Release.Version.Line().String(),
		"{repo}", run.Profile.GitHub.Repo,
		"{docsRepo}", run.Profile.GitHub.DocsRepo,
	).Replace(text)
}
