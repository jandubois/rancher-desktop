// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestACapturedCommandThatHangsIsStopped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hanging command needs sh")
	}

	defer func(was time.Duration) { capturedTimeout = was }(capturedTimeout)
	capturedTimeout = 100 * time.Millisecond

	// The trailing true keeps the shell from exec'ing sleep in its place, so
	// sleep runs as a child that holds the output pipe open after the shell
	// is killed.
	start := time.Now()
	_, err := tools{}.run(t.Context(), "sh", "-c", "sleep 10; true")

	if err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Errorf("a command that hung gave %v", err)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stopping a command that hung took %s", took)
	}
}

func TestACommandThatLeavesAChildBehindStillAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command needs sh")
	}

	// The shell exits at once, and the sleep it leaves in the background holds
	// the output pipe open.
	start := time.Now()
	output, err := tools{}.run(t.Context(), "sh", "-c", "sleep 10 & echo answered")

	if err != nil || strings.TrimSpace(string(output)) != "answered" {
		t.Errorf("a command that left a child behind gave %q and %v", output, err)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("a command that left a child behind took %s", took)
	}
}
