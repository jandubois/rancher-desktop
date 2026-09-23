// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// checksumSuffix ends the name of the file holding an asset's sha512.
const checksumSuffix = ".sha512sum"

// assetsDir is where a release's downloads go, under its cache directory.
const assetsDir = "assets"

// windowsSigningFile is the file the message for the Windows signer is
// written to, under the release's cache directory.
const windowsSigningFile = "windows-signing.md"

// linuxArtifact is what the package workflow calls the Linux build it
// uploads (.github/workflows/package.yaml).
const linuxArtifact = "Rancher Desktop-linux.zip"

// windowsArtifact is what the package workflow calls the Windows build it
// uploads (.github/workflows/package.yaml). It holds the zip the signer
// builds the installer from.
const windowsArtifact = "Rancher Desktop-win.zip"

// builtLinuxZip matches the name the build gives the Linux zip, which holds
// the version it stamped (packaging/electron-builder.yml, artifactName).
const builtLinuxZip = "rancher-desktop-*-linux.zip"

// linuxAsset is what the Linux zip is called on the release. The workflow
// that copies it to the OBS bucket builds its download URL from this name
// (.github/workflows/linux-release.yaml), so the release cannot keep the
// name the build gave it.
func linuxAsset(version Version) string {
	return "rancher-desktop-linux-v" + version.String() + ".zip"
}

// checkLinuxAssets reports whether the release has the Linux zip and the
// checksum that covers it.
func checkLinuxAssets(ctx context.Context, run *Run) (Answer, error) {
	zip := linuxAsset(run.Release.Version)

	return assetsPresent(ctx, run, "the Linux zip and its checksum", zip, zip+checksumSuffix)
}

// linuxAssetsReady holds the step until there is a release to upload to and
// a build to upload from.
func linuxAssetsReady(ctx context.Context, run *Run) (Answer, error) {
	if ready := waitFor(ctx, run, draftRelease, tagRelease); !ready.OK {
		return ready, nil
	}

	_, answer, err := builtArtifact(ctx, run, linuxArtifact)

	return answer, err
}

// linuxDownloadSize says what the download will cost before the action
// starts.
func linuxDownloadSize(ctx context.Context, run *Run) (string, error) {
	artifact, answer, err := builtArtifact(ctx, run, linuxArtifact)
	if err != nil || !answer.OK {
		return "", err
	}

	return fmt.Sprintf("%s is %.0f MiB, and downloading it is most of the wait.\n",
		linuxArtifact, float64(artifact.Size)/(1024*1024)), nil
}

// planLinuxAssets takes the Linux build from the tag's package run, gives it
// the name the release uses, writes its checksum and uploads what the
// release does not already have.
func planLinuxAssets(ctx context.Context, run *Run) ([]Operation, error) {
	tag := run.Release.Tag()
	name := linuxAsset(run.Release.Version)

	build, err := run.Repo.LatestRun(ctx, packageWorkflow, tag, run.Refs.Tags[run.Release.Version])
	if err != nil {
		return nil, err
	}

	if build == nil {
		return nil, fmt.Errorf("%s has no package run to take %s from", tag, linuxArtifact)
	}

	assets, err := run.Repo.ReleaseAssets(ctx, tag)
	if err != nil {
		return nil, err
	}

	names := []string{name, name + checksumSuffix}

	missing := absentAssets(assets, names...)
	if len(missing) == 0 {
		return nil, fmt.Errorf("%s already has %s and its checksum", tag, name)
	}

	kept := slices.DeleteFunc(names, func(asset string) bool {
		return slices.Contains(missing, asset)
	})

	dir, err := downloadDir(run)
	if err != nil {
		return nil, err
	}

	upload := []string{"release", "upload", tag, "--repo", run.Repo.repo}

	// GitHub keeps the name of a file whose upload stopped partway, and gh
	// refuses to upload under a name the release has unless given --clobber,
	// which deletes that file first.
	if slices.ContainsFunc(missing, func(asset string) bool {
		_, found := findAsset(assets, asset)
		return found
	}) {
		upload = append(upload, "--clobber")
	}

	upload = append(upload, under(dir, missing)...)

	operations := []Operation{
		command("", "gh", "run", "download", build.ID, "--repo", run.Repo.repo,
			"--name", linuxArtifact, "--dir", dir),
		{
			Description: "name the download " + name,
			Do: func(context.Context, *Run) error {
				return nameForRelease(dir, name)
			},
		},
		{
			// The checksum covers the zip under the name the release
			// gives it, so it is written after the rename.
			Description: "write " + name + checksumSuffix,
			Do: func(context.Context, *Run) error {
				return writeChecksum(filepath.Join(dir, name))
			},
		},
	}

	// A package run that ran again can build other bytes, and a checksum
	// from one build does not cover the zip from another. Once such a pair
	// is on the release the step reports done, so the check comes before the
	// upload.
	if len(kept) > 0 {
		operations = append(operations, Operation{
			Description: "check " + strings.Join(kept, " and ") + " on " + tag + " against the local copy",
			Do: func(ctx context.Context, run *Run) error {
				return verifyAgainstRelease(ctx, run, dir, kept)
			},
		})
	}

	return append(operations,
		command("", "gh", upload...),
		Operation{
			Description: "check what GitHub stored against what was uploaded",
			Do: func(ctx context.Context, run *Run) error {
				return verifyAgainstRelease(ctx, run, dir, missing)
			},
		},
	), nil
}

// windowsAsset is what the signed installer is called on the release.
// scripts/lib/installer-win32.tsx names the installer, and scripts/sign.ts
// writes the checksum beside it under that name, so neither file is
// renamed on the way to the release.
func windowsAsset(version Version) string {
	return "Rancher.Desktop.Setup." + version.String() + ".msi"
}

// checkWindowsAssets reports whether the release has the signed installer
// and the checksum that covers it.
func checkWindowsAssets(ctx context.Context, run *Run) (Answer, error) {
	msi := windowsAsset(run.Release.Version)

	return assetsPresent(ctx, run, "the Windows installer and its checksum", msi, msi+checksumSuffix)
}

// windowsAssetsReady holds the step until there is a release to upload to
// and a build to sign.
func windowsAssetsReady(ctx context.Context, run *Run) (Answer, error) {
	if ready := waitFor(ctx, run, draftRelease, tagRelease); !ready.OK {
		return ready, nil
	}

	_, answer, err := builtArtifact(ctx, run, windowsArtifact)

	return answer, err
}

// windowsSigningMessage is what the signer is sent. The signer never sees
// the step's instructions, so the message repeats them and names the run
// the build is in.
const windowsSigningMessage = `# Signing the Windows build of Rancher Desktop {version}

The build is the "{artifact}" artifact of this run:

    {runURL}

gh unpacks the artifact, so the zip arrives as the build wrote it:

    gh run download {run} --repo {repo} --name "{artifact}"

Sign it from a checkout of {tag} with its dependencies installed, and with
CSC_FINGERPRINT set to the fingerprint of the SUSE code-signing certificate.
docs/development/signing.md says how to read the fingerprint off the key and
what else the environment needs.

    yarn sign (Get-Item "Rancher Desktop*-win.zip")

That writes two files to dist:

    {msi}
    {checksum}

Upload both to the draft release under those names:

    gh release upload {tag} --repo {repo} dist/{msi} dist/{checksum}

Or send me both files and I will upload them.
`

// gatherWindowsSigning fills in the message for this release.
func gatherWindowsSigning(ctx context.Context, run *Run) (string, error) {
	tag := run.Release.Tag()
	msi := windowsAsset(run.Release.Version)

	commit, tagged := run.Refs.Tags[run.Release.Version]
	if !tagged {
		return "", fmt.Errorf("%s is not pushed, so no run has built the Windows zip", tag)
	}

	build, err := run.Repo.LatestRun(ctx, packageWorkflow, tag, commit)
	if err != nil {
		return "", err
	}

	if build == nil {
		return "", fmt.Errorf("%s has no package run to take %s from", tag, windowsArtifact)
	}

	return strings.NewReplacer(
		"{version}", run.Release.Version.String(),
		"{tag}", tag,
		"{repo}", run.Repo.repo,
		"{artifact}", windowsArtifact,
		"{run}", build.ID,
		"{runURL}", build.URL,
		"{msi}", msi,
		"{checksum}", msi+checksumSuffix,
	).Replace(windowsSigningMessage), nil
}

// assetsPresent reports whether the release has every named file in full.
// Its what argument names them together, for the line beside a step that has
// them all.
func assetsPresent(ctx context.Context, run *Run, what string, names ...string) (Answer, error) {
	tag := run.Release.Tag()

	assets, err := run.Repo.ReleaseAssets(ctx, tag)
	if errors.Is(err, errNotFound) {
		return Answer{Detail: "no release named " + tag}, nil
	}

	if err != nil {
		return Answer{}, err
	}

	for _, name := range names {
		asset, found := findAsset(assets, name)

		switch {
		case !found:
			return Answer{Detail: fmt.Sprintf("%s has no %s", tag, name)}, nil
		case asset.State != uploaded:
			return Answer{Detail: fmt.Sprintf("%s on %s is %s", name, tag, asset.State)}, nil
		}
	}

	return Answer{OK: true, Detail: fmt.Sprintf("%s has %s", tag, what)}, nil
}

// absentAssets are the named files the release does not have in full, which
// are the ones an upload has to send.
func absentAssets(assets []releaseAsset, names ...string) []string {
	var absent []string

	for _, name := range names {
		if asset, found := findAsset(assets, name); !found || asset.State != uploaded {
			absent = append(absent, name)
		}
	}

	return absent
}

// findAsset is the release's file with a name.
func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}

	return releaseAsset{}, false
}

// builtArtifact is the file a step takes from the tag's package run, with
// how that run is going.
func builtArtifact(ctx context.Context, run *Run, name string) (Artifact, Answer, error) {
	tag := run.Release.Tag()

	// The caller has already waited for the tag step, so the tag is in the
	// refs and the run is the one the tag push started.
	build, err := run.Repo.LatestRun(ctx, packageWorkflow, tag, run.Refs.Tags[run.Release.Version])
	if err != nil {
		return Artifact{}, Answer{}, err
	}

	if build == nil {
		return Artifact{}, Answer{Waiting: true, Detail: "no package run for " + tag + " yet"}, nil
	}

	artifacts, err := run.Repo.Artifacts(ctx, build.ID)
	if err != nil {
		return Artifact{}, Answer{}, err
	}

	artifact, built := findArtifact(artifacts, name)

	switch {
	case built && !artifact.Expired:
		return artifact, Answer{OK: true}, nil
	case built:
		return artifact, Answer{Detail: fmt.Sprintf(
			"%s has expired; rerun the package run for %s to build it again: %s",
			name, tag, build.URL)}, nil
	case !build.Finished():
		return artifact, Answer{Waiting: true, Detail: fmt.Sprintf(
			"the package run for %s is %s: %s", tag, build.State(), build.URL)}, nil
	default:
		return artifact, Answer{Detail: fmt.Sprintf(
			"the package run for %s built no %s: %s", tag, name, build.URL)}, nil
	}
}

// findArtifact is the run's file with a name.
func findArtifact(artifacts []Artifact, name string) (Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}

	return Artifact{}, false
}

// downloadDir is where a release's assets are downloaded to. Nothing removes
// them, so a release's downloads accumulate until somebody deletes its cache
// directory.
func downloadDir(run *Run) (string, error) {
	cache, err := CacheDir(run.Profile.Name, run.Release.Version)
	if err != nil {
		return "", err
	}

	return filepath.Join(cache, assetsDir), nil
}

// under puts the download directory in front of each name, for a command
// that takes files.
func under(dir string, names []string) []string {
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}

	return paths
}

// nameForRelease gives the download the name the release uses. gh unpacks
// the artifact, so the zip arrives under the name the build stamped it with.
func nameForRelease(dir, name string) error {
	built, err := filepath.Glob(filepath.Join(dir, builtLinuxZip))
	if err != nil {
		return err
	}

	if len(built) != 1 {
		return fmt.Errorf("%s holds %d files named %s, and the download is one file",
			dir, len(built), builtLinuxZip)
	}

	return os.Rename(built[0], filepath.Join(dir, name))
}

// writeChecksum writes an asset's sha512 beside it, two spaces between the
// hash and the name, which is the form every released Linux checksum has. The
// signed platforms' come from scripts/sign.ts and use an asterisk instead.
func writeChecksum(path string) error {
	sum, err := hashOf(path, sha512.New())
	if err != nil {
		return err
	}

	line := fmt.Sprintf("%s  %s\n", sum, filepath.Base(path))

	return os.WriteFile(path+checksumSuffix, []byte(line), 0o644)
}

// verifyAgainstRelease checks the release's copy of each named file against
// the one in dir. GitHub reports each asset's sha256, so a file that arrived
// short, or one the release has from another build, is caught here instead
// of by whoever downloads the release.
func verifyAgainstRelease(ctx context.Context, run *Run, dir string, names []string) error {
	tag := run.Release.Tag()

	assets, err := run.Repo.ReleaseAssetsNow(ctx, tag)
	if err != nil {
		return err
	}

	for _, name := range names {
		asset, found := findAsset(assets, name)
		if !found {
			return fmt.Errorf("%s is not on %s", name, tag)
		}

		sum, err := hashOf(filepath.Join(dir, name), sha256.New())
		if err != nil {
			return err
		}

		if asset.Digest != "sha256:"+sum {
			return fmt.Errorf("%s on %s is %s, and the local copy is sha256:%s; delete the release's copy and run the step again",
				name, tag, asset.Digest, sum)
		}
	}

	return nil
}

// hashOf is a file's hash, read in one pass so that a release asset never
// has to fit in memory.
func hashOf(path string, digest hash.Hash) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}

	defer file.Close()

	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}
