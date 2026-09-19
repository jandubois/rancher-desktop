// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
}

// Doc is a step's prose. The placeholders {version}, {tag}, {branch}, {line}
// and {repo} stand for the release's own values, which the detail pane fills
// in and the README keeps, so the README describes every release.
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

	probed map[string]Status
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
		return Status{State: Done, Detail: answer.Detail}
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
	).Replace(text)
}
