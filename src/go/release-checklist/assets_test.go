// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// testRunID is the package run the test release's tag started.
	testRunID = "30474476005"
	// testZip is what the Linux zip is called on the test release.
	testZip = "rancher-desktop-linux-v1.25.0.zip"
	// testArtifactSize is the size of a real Linux artifact.
	testArtifactSize = 705811481
)

// artifactsQuery is the command that lists what a run built.
func artifactsQuery(runID string) string {
	return "gh api repos/" + testRepo + "/actions/runs/" + runID + "/artifacts?per_page=100"
}

// artifactsJSON is what GitHub answers for a run that built the Linux zip,
// in the shape a real lookup returns.
func artifactsJSON(expired bool) string {
	return fmt.Sprintf(`{"total_count":1,"artifacts":[{"name":%q,"expired":%t,"size_in_bytes":%d}]}`,
		linuxArtifact, expired, testArtifactSize)
}

// draftWithAssets is what gh answers for a draft release carrying files.
func draftWithAssets(t *testing.T, assets ...releaseAsset) string {
	t.Helper()

	answer, err := json.Marshal(releaseView{IsDraft: true, Assets: assets})
	if err != nil {
		t.Fatal(err)
	}

	return string(answer)
}

// stored is an asset GitHub has the whole of, under a digest nothing in
// these tests hashes to.
func stored(name string) releaseAsset {
	return releaseAsset{Name: name, State: uploaded, Digest: "sha256:" + strings.Repeat("a", 64)}
}

// uploadRun is a refresh of a release whose tag is pushed, carrying the
// assets and the package run artifacts it is given.
func uploadRun(t *testing.T, release, artifacts string) *Run {
	t.Helper()

	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
	answers["gh release view v1.25.0 --repo "+testRepo+" --json "+releaseFields] = release
	answers[runsQuery("v1.25.0")] = packageRunJSON("completed", "success")
	answers[artifactsQuery(testRunID)] = artifacts

	return tagRun(t, &fakeTools{output: answers}, map[Version]string{testRelease: testHead})
}

// inACacheOfItsOwn points the release's cache directory at a temporary one,
// on every platform.
func inACacheOfItsOwn(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("LocalAppData", home)
}

func TestLinuxAssetsAreDoneWhenTheReleaseHasBoth(t *testing.T) {
	run := uploadRun(t,
		draftWithAssets(t, stored(testZip), stored(testZip+checksumSuffix)),
		artifactsJSON(false))

	if status := run.Status(context.Background(), linuxAssets); status.State != Done {
		t.Errorf("the Linux assets were %s: %s", status.State, status.Detail)
	}
}

func TestLinuxAssetsAreAvailableWhileTheChecksumIsMissing(t *testing.T) {
	run := uploadRun(t,
		draftWithAssets(t, stored(testZip)),
		artifactsJSON(false))

	status := run.Status(context.Background(), linuxAssets)
	if status.State != Available {
		t.Fatalf("a release missing the checksum was %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, checksumSuffix) {
		t.Errorf("the detail does not name what is missing: %s", status.Detail)
	}
}

func TestAnAssetStillArrivingIsNotOnTheRelease(t *testing.T) {
	half := releaseAsset{Name: testZip + checksumSuffix, State: "starting"}

	run := uploadRun(t,
		draftWithAssets(t, stored(testZip), half),
		artifactsJSON(false))

	// GitHub names an asset as soon as an upload starts, so the name alone
	// would report a release nobody can download from as finished.
	status := run.Status(context.Background(), linuxAssets)
	if status.State != Available {
		t.Fatalf("an upload that stopped partway was %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "starting") {
		t.Errorf("the detail does not say what state the file is in: %s", status.Detail)
	}
}

func TestLinuxAssetsAreBlockedOnceTheArtifactHasExpired(t *testing.T) {
	run := uploadRun(t,
		draftWithAssets(t),
		artifactsJSON(true))

	status := run.Status(context.Background(), linuxAssets)
	if status.State != Blocked {
		t.Fatalf("an expired artifact was %s: %s", status.State, status.Detail)
	}

	// Only another run can put an expired artifact back.
	if !strings.Contains(status.Detail, "rerun") {
		t.Errorf("the detail does not say how to get the file back: %s", status.Detail)
	}
}

func TestLinuxAssetsWaitWhileTheRunIsStillBuilding(t *testing.T) {
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
	answers["gh release view v1.25.0 --repo "+testRepo+" --json "+releaseFields] = draftWithAssets(t)
	answers[runsQuery("v1.25.0")] = packageRunJSON("in_progress", "")
	answers[artifactsQuery(testRunID)] = `{"total_count":0,"artifacts":[]}`

	run := tagRun(t, &fakeTools{output: answers}, map[Version]string{testRelease: testHead})

	status := run.Status(context.Background(), linuxAssets)
	if status.State != Waiting {
		t.Fatalf("a run that had not uploaded its zip yet was %s: %s", status.State, status.Detail)
	}
}

func TestLinuxAssetsWaitForSomewhereToUploadTo(t *testing.T) {
	answers := readyToTag(packageRunJSON("completed", "success"))
	answers[contentsQuery("v1.25.0")] = strings.Replace(manifest, "1.24.0", "1.25.0", 1)
	answers["gh release view v1.25.0 --repo "+testRepo+" --json "+releaseFields] = ""
	answers[runsQuery("v1.25.0")] = packageRunJSON("completed", "success")
	answers[artifactsQuery(testRunID)] = artifactsJSON(false)

	tools := &fakeTools{output: answers, stderr: map[string]string{
		"gh release view v1.25.0 --repo " + testRepo + " --json " + releaseFields: "release not found",
	}}

	run := tagRun(t, tools, map[Version]string{testRelease: testHead})

	status := run.Status(context.Background(), linuxAssets)
	if status.State != Blocked {
		t.Fatalf("uploading to a release that does not exist was %s: %s", status.State, status.Detail)
	}

	if !strings.Contains(status.Detail, "Draft release") {
		t.Errorf("the detail does not say what the step waits for: %s", status.Detail)
	}
}

func TestUploadingSendsOnlyWhatTheReleaseIsMissing(t *testing.T) {
	inACacheOfItsOwn(t)

	run := uploadRun(t,
		draftWithAssets(t, stored(testZip)),
		artifactsJSON(false))

	operations, err := planLinuxAssets(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}

	var sent []string

	for _, operation := range operations {
		if operation.Command != "gh" || len(operation.Args) == 0 || operation.Args[0] != "release" {
			continue
		}

		for _, argument := range operation.Args {
			if strings.HasSuffix(argument, ".zip") || strings.HasSuffix(argument, checksumSuffix) {
				sent = append(sent, filepath.Base(argument))
			}
		}
	}

	// Sending the zip again would upload the whole file for nothing.
	if len(sent) != 1 || sent[0] != testZip+checksumSuffix {
		t.Errorf("the upload sends %v, and the release lacks only the checksum", sent)
	}
}

func TestUploadingRefusesWhenTheReleaseHasEverything(t *testing.T) {
	inACacheOfItsOwn(t)

	run := uploadRun(t,
		draftWithAssets(t, stored(testZip), stored(testZip+checksumSuffix)),
		artifactsJSON(false))

	if _, err := planLinuxAssets(context.Background(), run); err == nil {
		t.Error("a release with both files was given an upload to run")
	}
}

func TestTheDownloadTakesTheNameTheReleaseUses(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "rancher-desktop-1.25.0-linux.zip"), []byte("a build"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := nameForRelease(dir, testZip); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, testZip)); err != nil {
		t.Errorf("the download is not under the release's name: %v", err)
	}
}

func TestRenamingRefusesWhenTheDownloadIsNotOneFile(t *testing.T) {
	dir := t.TempDir()

	if err := nameForRelease(dir, testZip); err == nil {
		t.Error("an empty directory was taken for a download")
	}

	for _, name := range []string{"rancher-desktop-1.25.0-linux.zip", "rancher-desktop-1.25.1-linux.zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("a build"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := nameForRelease(dir, testZip); err == nil {
		t.Error("two builds were renamed to the one name the release uses")
	}
}

func TestTheChecksumIsTheFormTheReleasesCarry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testZip)
	content := []byte("a build")

	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeChecksum(path); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(path + checksumSuffix)
	if err != nil {
		t.Fatal(err)
	}

	if want := fmt.Sprintf("%x  %s\n", sha512.Sum512(content), testZip); string(written) != want {
		t.Errorf("the checksum reads %q, and sha512sum writes %q", written, want)
	}
}

func TestAnUploadThatArrivedShortIsCaught(t *testing.T) {
	run := uploadRun(t,
		draftWithAssets(t, stored(testZip)),
		artifactsJSON(false))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testZip), []byte("a build"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := verifyUploaded(context.Background(), run, dir, []string{testZip})
	if err == nil {
		t.Fatal("a release holding other bytes than the ones uploaded passed")
	}

	if !strings.Contains(err.Error(), testZip) {
		t.Errorf("the failure does not name the file: %v", err)
	}
}

func TestAnUploadThatArrivedWholePasses(t *testing.T) {
	content := []byte("a build")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))

	run := uploadRun(t,
		draftWithAssets(t, releaseAsset{Name: testZip, State: uploaded, Digest: digest}),
		artifactsJSON(false))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testZip), content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyUploaded(context.Background(), run, dir, []string{testZip}); err != nil {
		t.Errorf("a file GitHub stored whole was reported short: %v", err)
	}
}

func TestARunWithMoreFilesThanAPageIsNotReadAsBuildingNothing(t *testing.T) {
	run := uploadRun(t,
		draftWithAssets(t),
		`{"total_count":140,"artifacts":[{"name":"bats.tar.gz","expired":false,"size_in_bytes":1}]}`)

	// The artifact could be on the page nobody read, and reporting it as
	// one the run never built would send somebody to rebuild a good run.
	if status := run.Status(context.Background(), linuxAssets); status.State != Unknown {
		t.Errorf("a truncated listing was %s: %s", status.State, status.Detail)
	}
}
