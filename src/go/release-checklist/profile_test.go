// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// filledProfile walks Profile by reflection, gives every resource field a
// value of its own, and returns those values keyed by the path that
// Resources() uses for each. A field added to Profile reaches this walk even
// when Resources() forgets it.
func filledProfile(t *testing.T) (*Profile, map[string]string) {
	t.Helper()

	profile := &Profile{Name: "filled"}
	values := map[string]string{}

	var walk func(v reflect.Value, prefix string)
	walk = func(v reflect.Value, prefix string) {
		for i := range v.NumField() {
			field := v.Type().Field(i)
			path := prefix + field.Tag.Get("yaml")
			if path == "name" {
				continue
			}

			value := v.Field(i)
			if value.Kind() == reflect.Pointer && value.Type().Elem().Kind() == reflect.Struct {
				value.Set(reflect.New(value.Type().Elem()))
				value = value.Elem()
			}

			switch value.Kind() {
			case reflect.String:
				value.SetString("value-of-" + path)
				values[path] = value.String()
			case reflect.Struct:
				walk(value, path+".")
			default:
				t.Fatalf("Profile field %s is a %s; teach both this walk and Resources() about it", path, value.Kind())
			}
		}
	}
	walk(reflect.ValueOf(profile).Elem(), "")

	return profile, values
}

func TestResourcesNamesEveryProfileField(t *testing.T) {
	profile, want := filledProfile(t)

	if got := profile.Resources(); !maps.Equal(got, want) {
		t.Errorf("Resources() gave %v, want %v", got, want)
	}
}

func TestProductionProfileNamesTheRealResources(t *testing.T) {
	profile, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	_, fields := filledProfile(t)

	resources := profile.Resources()
	for field := range fields {
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

func TestProfileRefusesToLoadWithoutARepository(t *testing.T) {
	_, err := parseProfile([]byte("name: fork\ngithub:\n  docsRepo: me/docs.rancherdesktop.io\n"), "test")
	if err == nil || !strings.Contains(err.Error(), "github.repo") {
		t.Errorf("a profile with no github.repo gave %v", err)
	}
}

func TestProfileRefusesToReuseAProductionResource(t *testing.T) {
	production, err := LoadProfile(productionName)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		obs   string
		field string
	}{
		// A fork profile that changed the repositories but forgot the OBS
		// project would publish into the real one.
		{"  devProject: home:me:rd-release-dev\n  stableProject: isv:Rancher:stable\n", "obs.stableProject"},
		// Production's stable project is just as real under another field.
		{"  devProject: isv:Rancher:stable\n  stableProject: home:me:rd-release-stable\n", "obs.devProject"},
		// A fork with OBS projects of its own passes.
		{"  devProject: home:me:rd-release-dev\n  stableProject: home:me:rd-release-stable\n", ""},
	} {
		fork, err := parseProfile([]byte(""+
			"name: fork\n"+
			"github:\n"+
			"  repo: me/rancher-desktop\n"+
			"  docsRepo: me/docs.rancherdesktop.io\n"+
			"obs:\n"+tc.obs), "test")
		if err != nil {
			t.Fatal(err)
		}

		err = fork.RefuseProductionResources(production)
		if tc.field == "" {
			if err != nil {
				t.Errorf("a fork of its own gave %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("reusing the production project as %s gave %v", tc.field, err)
		}
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
	writeProfile(t, "Production", "name: Production\ngithub:\n  repo: example-org/rancher-desktop-rehearsal\n")

	_, err := LoadProfile("Production")
	if err == nil || !strings.Contains(err.Error(), "only in case") {
		t.Errorf("loading the profile %q gave %v", "Production", err)
	}
}

func TestLoadProfileChecksTheProfileOnDisk(t *testing.T) {
	const repo = "  repo: example-org/rancher-desktop-rehearsal\n"

	for _, tc := range []struct {
		yaml string
		want string
	}{
		// The name matches and the repository passes the owner check, so only
		// the reused OBS project can be refused.
		{"name: rehearsal\ngithub:\n" + repo + "obs:\n  stableProject: isv:Rancher:stable\n", "obs.stableProject"},
		{"name: other\ngithub:\n" + repo, `names the profile "other", not "rehearsal"`},
		// A profile of its own loads, so the errors above come from the checks.
		{"name: rehearsal\ngithub:\n" + repo, ""},
	} {
		inAConfigOfItsOwn(t)
		writeProfile(t, "rehearsal", tc.yaml)

		profile, err := LoadProfile("rehearsal")
		if tc.want == "" {
			if err != nil {
				t.Errorf("a profile of its own gave %v", err)
			} else if profile.Name != "rehearsal" || profile.GitHub.Repo != "example-org/rancher-desktop-rehearsal" {
				t.Errorf("a profile of its own loaded as %+v", profile)
			}
		} else if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("loading %q gave %v, want an error containing %s", tc.yaml, err, tc.want)
		}
	}
}
