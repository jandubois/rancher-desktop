// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// commander runs the command line tools the steps drive. Everything that
// needs credentials goes through one of them, so the credentials stay in
// their own stores and the tool keeps none.
//
// The tests answer with output captured from real runs instead.
type commander interface {
	// run returns the command's standard output. A command that exits
	// non-zero returns a *commandFailure.
	run(ctx context.Context, name string, args ...string) ([]byte, error)
	// runIn runs the command in a directory, for the worktrees the actions
	// work in.
	runIn(ctx context.Context, dir, name string, args ...string) ([]byte, error)
	// installed reports whether the command is on the PATH.
	installed(name string) bool
}

// commandFailure is a command that ran and exited non-zero. Callers read
// Stderr to tell an expected answer, such as GitHub reporting no release for
// a tag, from a real failure.
type commandFailure struct {
	Command string
	Stderr  string
	Err     error
}

func (f *commandFailure) Error() string {
	if f.Stderr == "" {
		return fmt.Sprintf("%s: %v", f.Command, f.Err)
	}

	return fmt.Sprintf("%s: %v: %s", f.Command, f.Err, f.Stderr)
}

func (f *commandFailure) Unwrap() error { return f.Err }

// says reports whether the command's standard error mentions the text, for
// callers matching a known answer.
func (f *commandFailure) says(text string) bool {
	return strings.Contains(strings.ToLower(f.Stderr), strings.ToLower(text))
}

// missing reports whether GitHub answered that the thing is not there, which
// is an answer a check reads rather than a failure. gh ends every API error
// with the status, and the prose before it varies: a missing branch gives
// "No commit found for the ref", not "Not Found".
func (f *commandFailure) missing() bool {
	return f.says("(HTTP 404)")
}

// tools runs the real command line tools.
type tools struct{}

func (t tools) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return t.runIn(ctx, "", name, args...)
}

func (tools) runIn(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	// The command and its arguments come from the step definitions and the
	// profile, and never reach a shell, so nothing is word-split or globbed.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &commandFailure{
			Command: strings.Join(append([]string{name}, args...), " "),
			Stderr:  strings.TrimSpace(stderr.String()),
			Err:     err,
		}
	}

	return stdout.Bytes(), nil
}

func (tools) installed(name string) bool {
	_, err := exec.LookPath(name)

	return err == nil
}
