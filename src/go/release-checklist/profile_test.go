// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
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
