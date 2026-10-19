// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionProfileNamesTheRealResources(t *testing.T) {
	profile, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	resources := profile.Resources()
	for _, field := range []string{
		"github.repo", "github.docsRepo", "docs.site",
		"obs.devProject", "obs.stableProject",
		"screenshots.bucket", "responder.url",
	} {
		if resources[field] == "" {
			t.Errorf("the production profile names no %s", field)
		}
	}
}

func TestProfileRejectsFieldsItDoesNotKnow(t *testing.T) {
	_, err := parseProfile([]byte("name: fork\ngithub:\n  repo: me/rancher-desktop\nobs:\n  devProjekt: home:me\n"), "test")
	if err == nil || !strings.Contains(err.Error(), "devProjekt") {
		t.Errorf("a misspelled field gave %v", err)
	}
}

func TestProfileRefusesToReuseAProductionResource(t *testing.T) {
	production, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	// A fork profile that changed the repositories but forgot the OBS
	// project would publish into the real one.
	fork, err := parseProfile([]byte(""+
		"name: fork\n"+
		"github:\n"+
		"  repo: me/rancher-desktop\n"+
		"  docsRepo: me/docs.rancherdesktop.io\n"+
		"obs:\n"+
		"  devProject: home:me:rd-release-dev\n"+
		"  stableProject: isv:Rancher:stable\n"), "test")
	if err != nil {
		t.Fatal(err)
	}

	err = fork.RefuseProductionResources(production)
	if err == nil || !strings.Contains(err.Error(), "obs.stableProject") {
		t.Errorf("reusing the production project gave %v", err)
	}

	fork.OBS.StableProject = "home:me:rd-release-stable"

	if err := fork.RefuseProductionResources(production); err != nil {
		t.Errorf("a fork of its own gave %v", err)
	}
}

func TestProductionProfileIsAllowedItsOwnResources(t *testing.T) {
	production, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	if err := production.RefuseProductionResources(production); err != nil {
		t.Errorf("production refused itself: %v", err)
	}
}

func TestProfileRefusesAProductionRepositoryWrittenInAnotherCase(t *testing.T) {
	production, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	// GitHub ignores case in repository names, so this names the production
	// repository.
	fork, err := parseProfile([]byte("name: fork\ngithub:\n  repo: "+strings.ToUpper(production.GitHub.Repo)+"\n"), "test")
	if err != nil {
		t.Fatal(err)
	}

	err = fork.RefuseProductionResources(production)
	if err == nil || !strings.Contains(err.Error(), "github.repo") {
		t.Errorf("the production repository in capitals gave %v", err)
	}
}

func TestProfileRefusesEveryRepositoryOfProductionsOwner(t *testing.T) {
	production, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	for field, github := range map[string]string{
		// GitHub redirects rancher-sandbox/rd to the production repository.
		"github.repo":     "  repo: rancher-sandbox/rd\n",
		"github.docsRepo": "  repo: example-org/rancher-desktop-rehearsal\n  docsRepo: Rancher-Sandbox/docs\n",
	} {
		profile, err := parseProfile([]byte("name: rehearsal\ngithub:\n"+github), "test")
		if err != nil {
			t.Fatal(err)
		}

		err = profile.RefuseProductionResources(production)
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("a rancher-sandbox repository as %s gave %v", field, err)
		}
	}
}

// inAConfigOfItsOwn points the user's config directory at a temporary one,
// on every platform.
func inAConfigOfItsOwn(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", home)
}

func TestProfileRefusesANameThatDiffersFromProductionOnlyInCase(t *testing.T) {
	inAConfigOfItsOwn(t)

	dir, err := ProfileDir("Production")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: Production\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n"
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadProfile("Production")
	if err == nil || !strings.Contains(err.Error(), "only in case") {
		t.Errorf("loading the profile %q gave %v", "Production", err)
	}
}
