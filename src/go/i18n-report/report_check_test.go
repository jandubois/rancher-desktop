// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSourceReportsUnusedAsFindings(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true)

	// No source file references status.checking, so it is unused.
	err := reportCheckSource(io.Discard, dir)
	if err == nil {
		t.Fatal("expected findings for an unused key")
	}
	if !errors.Is(err, errFindings) {
		t.Errorf("unused key should be a findings error, got: %v", err)
	}
}

func TestCheckSourcePassesWhenKeyReferenced(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true)

	srcDir := filepath.Join(dir, "pkg", "rancher-desktop", "components")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := "<template>\n  <span v-t=\"'status.checking'\" />\n</template>\n"
	if err := os.WriteFile(filepath.Join(srcDir, "Sample.vue"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reportCheckSource(io.Discard, dir); err != nil {
		t.Errorf("expected source gate to pass, got: %v", err)
	}
}

func TestCheckPolicyFailureIsFindings(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	de := "status:\n  checking: Wird geprüft…\n  removed: Veraltet\n"
	dir := setupLocaleTestRepo(t, enUS, de, true)

	err := reportCheckPolicy(io.Discard, dir, "de", "experimental")
	if !errors.Is(err, errFindings) {
		t.Errorf("policy failure should be a findings error, got: %v", err)
	}
}

func TestCheckPolicyExperimentalPasses(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n  done: Done\n"
	// de has only 1 key — missing keys are OK for experimental.
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true)

	err := reportCheckPolicy(io.Discard, dir, "de", "experimental")
	if err != nil {
		t.Errorf("experimental should pass with missing keys, got: %v", err)
	}
}

func TestCheckPolicyShippingFailsMissing(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n  done: Done\n"
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true, "shipping")

	err := reportCheckPolicy(io.Discard, dir, "de", "shipping")
	if err == nil {
		t.Error("shipping should fail with missing keys")
	}
}

func TestCheckPolicyShippingPasses(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n  done: Done\n"
	de := "status:\n  checking: Wird geprüft…\n  done: Fertig\n"
	dir := setupLocaleTestRepo(t, enUS, de, true, "shipping")

	err := reportCheckPolicy(io.Discard, dir, "de", "shipping")
	if err != nil {
		t.Errorf("shipping should pass with complete translation, got: %v", err)
	}
}

func TestCheckPolicyShippingFailsExperimentalStatus(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true, "experimental")

	err := reportCheckPolicy(io.Discard, dir, "de", "shipping")
	if err == nil {
		t.Error("shipping should fail for experimental-status locale")
	}
}

func TestCheckPolicyExperimentalFailsStale(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	// de has a stale key not in en-us.
	de := "status:\n  checking: Wird geprüft…\n  removed: Veraltet\n"
	dir := setupLocaleTestRepo(t, enUS, de, true)

	err := reportCheckPolicy(io.Discard, dir, "de", "experimental")
	if err == nil {
		t.Error("experimental should fail with stale keys")
	}
}

func TestCheckPolicyShippingFailsDrift(t *testing.T) {
	enUS := "status:\n  checking: Checking...\n"
	de := "status:\n  checking: Wird geprüft…\n"
	dir := setupLocaleTestRepo(t, enUS, de, true, "shipping")

	// Change English after metadata was generated.
	transDir := filepath.Join(dir, "pkg", "rancher-desktop", "assets", "translations")
	os.WriteFile(filepath.Join(transDir, "en-us.yaml"), []byte("status:\n  checking: Verifying...\n"), 0644)

	err := reportCheckPolicy(io.Discard, dir, "de", "shipping")
	if err == nil {
		t.Error("shipping should fail with drifted keys")
	}
}
