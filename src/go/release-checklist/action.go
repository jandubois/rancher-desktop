// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunAction shows every operation a step would perform, asks once, and runs
// them in order. It stops at the first failure, because the operations after
// it would build on work that did not happen.
func RunAction(ctx context.Context, step *Step, run *Run, in io.Reader, out io.Writer) error {
	if step.Action == nil {
		return fmt.Errorf("step %s has no automation; its instructions say how to do it", step.ID)
	}

	if status := run.Status(ctx, step); status.State != Available {
		return fmt.Errorf("step %s is %s: %s", step.ID, status.State, status.Detail)
	}

	operations, err := step.Action.Plan(ctx, run)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s\n\n", fill(step.Action.Title, run))

	if step.Action.Summary != nil {
		summary, err := step.Action.Summary(ctx, run)
		if err != nil {
			return err
		}

		if summary != "" {
			fmt.Fprintf(out, "%s\n", summary)
		}
	}

	for _, operation := range operations {
		fmt.Fprintf(out, "    %s\n", operation.Description)
	}

	question := fmt.Sprintf("Run these %d operations?", len(operations))
	if len(operations) == 1 {
		question = "Run this operation?"
	}

	fmt.Fprintf(out, "\n%s [y/N] ", question)

	if !confirmed(in) {
		fmt.Fprintln(out, "Nothing was run.")

		return nil
	}

	for _, operation := range operations {
		fmt.Fprintf(out, "\n%s\n", operation.Description)

		if err := operation.perform(ctx, run); err != nil {
			return fmt.Errorf("%s: %w", operation.Description, err)
		}
	}

	return nil
}

// confirmed reads the answer to the one question an action asks. Anything but
// yes leaves the release alone.
func confirmed(in io.Reader) bool {
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func (o *Operation) perform(ctx context.Context, run *Run) error {
	if o.Do != nil {
		return o.Do(ctx, run)
	}

	_, err := run.Tools.runIn(ctx, o.Dir, o.Command, o.Args...)

	return err
}

// command is an operation that runs an external command, described by the
// command line itself, so the confirmation shows exactly what will run.
func command(dir, name string, args ...string) Operation {
	shown := make([]string, 0, len(args)+1)
	shown = append(shown, name)

	for _, arg := range args {
		shown = append(shown, quote(arg))
	}

	return Operation{
		Description: strings.Join(shown, " "),
		Command:     name,
		Args:        args,
		Dir:         dir,
	}
}

// quote shows an argument the way a reader would have to type it, so a value
// with spaces in it cannot be read as several arguments.
func quote(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\") {
		return strconv.Quote(arg)
	}

	return arg
}

// GatherFacts writes the reference material a step's manual work is done
// from, and says where it went. Gathering reads the release and changes
// nothing, so it asks no question first.
func GatherFacts(ctx context.Context, step *Step, run *Run) (string, error) {
	if step.Facts == nil {
		return "", fmt.Errorf("step %s gathers nothing; press i for its instructions", step.ID)
	}

	material, err := step.Facts.Gather(ctx, run)
	if err != nil {
		return "", err
	}

	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(cache, cachePermissions); err != nil {
		return "", err
	}

	path := filepath.Join(cache, step.Facts.File)
	if err := os.WriteFile(path, []byte(material), 0o644); err != nil {
		return "", err
	}

	return path, nil
}
