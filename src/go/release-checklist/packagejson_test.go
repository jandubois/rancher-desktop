// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"strings"
	"testing"
)

// manifest has the shape of the repository's own package.json: the version
// on its own line, five spaces of neighbours around it.
const manifest = `{
  "name": "rancher-desktop",
  "private": true,
  "description": "Kubernetes and container management on the desktop",
  "version": "1.24.0",
  "author": "SUSE",
  "scripts": {
    "dev": "node scripts/ts-wrapper.js scripts/dev.ts"
  }
}
`

func TestReadPackageVersion(t *testing.T) {
	version, err := readPackageVersion([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}

	if version != (Version{Major: 1, Minor: 24}) {
		t.Errorf("read %s", version)
	}

	if _, err := readPackageVersion([]byte(`{"name": "rancher-desktop"}`)); err == nil {
		t.Error("a package.json with no version gave one")
	}
}

func TestSetPackageVersionChangesTheOneLine(t *testing.T) {
	updated, err := setPackageVersion([]byte(manifest), Version{Major: 1, Minor: 25})
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Replace(manifest, `"version": "1.24.0"`, `"version": "1.25.0"`, 1)
	if string(updated) != want {
		t.Errorf("the bump rewrote more than the version:\n%s", updated)
	}
}

func TestSetPackageVersionRefusesAFileItCannotPlace(t *testing.T) {
	two := strings.Replace(manifest, `  "author": "SUSE",`, `  "version": "0.0.1",`, 1)
	if _, err := setPackageVersion([]byte(two), Version{Major: 1, Minor: 25}); err == nil {
		t.Error("a package.json with two version fields was edited anyway")
	}

	if _, err := setPackageVersion([]byte(`{"name": "x"}`), Version{Major: 1, Minor: 25}); err == nil {
		t.Error("a package.json with no version field was edited anyway")
	}
}
