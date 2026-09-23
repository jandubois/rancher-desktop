// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// productionName is the profile a plain `yarn release` uses.
const productionName = "production"

//go:embed production.yaml
var productionYAML []byte

// Profile names every resource outside this machine that a release touches.
// Step code never spells out a repository, an OBS project or a bucket; it
// reads them here, so one profile switch points a whole release at a fork.
//
// A section left out turns its steps off: a profile with no obs section
// skips the OBS steps rather than failing them.
type Profile struct {
	Name        string           `yaml:"name"`
	GitHub      GitHubResources  `yaml:"github"`
	Docs        *DocsResources   `yaml:"docs"`
	OBS         *OBSResources    `yaml:"obs"`
	Screenshots *S3Resources     `yaml:"screenshots"`
	Responder   *ResponderTarget `yaml:"responder"`
}

// GitHubResources are the repositories a release writes to.
type GitHubResources struct {
	Repo     string `yaml:"repo"`
	DocsRepo string `yaml:"docsRepo"`
}

// DocsResources is the documentation site a release publishes to.
type DocsResources struct {
	Site string `yaml:"site"`
}

// OBSResources are the Open Build Service projects that build the Linux
// packages.
type OBSResources struct {
	DevProject    string `yaml:"devProject"`
	StableProject string `yaml:"stableProject"`
}

// S3Resources is the bucket the documentation screenshots are served from.
type S3Resources struct {
	Bucket string `yaml:"bucket"`
}

// ResponderTarget is the upgrade responder that offers the release to
// installed copies of Rancher Desktop.
type ResponderTarget struct {
	URL string `yaml:"url"`
}

// LoadProfile reads the named profile. The production profile is built into
// the binary; every other name comes from the user's config directory, so a
// fork profile with personal accounts in it never reaches the repository.
func LoadProfile(name string) (*Profile, error) {
	if name == productionName {
		return parseProfile(productionYAML, "the built-in production profile")
	}

	dir, err := ProfileDir(name)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "profile.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %q: %w", name, err)
	}

	profile, err := parseProfile(data, path)
	if err != nil {
		return nil, err
	}

	if profile.Name != name {
		return nil, fmt.Errorf("%s names the profile %q, not %q", path, profile.Name, name)
	}

	production, err := parseProfile(productionYAML, "the built-in production profile")
	if err != nil {
		return nil, err
	}

	if err := profile.RefuseProductionResources(production); err != nil {
		return nil, err
	}

	return profile, nil
}

func parseProfile(data []byte, source string) (*Profile, error) {
	profile := &Profile{}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	if err := decoder.Decode(profile); err != nil {
		return nil, fmt.Errorf("reading %s: %w", source, err)
	}

	if profile.Name == "" {
		return nil, fmt.Errorf("%s has no name", source)
	}

	if profile.GitHub.Repo == "" {
		return nil, fmt.Errorf("%s names no github.repo", source)
	}

	return profile, nil
}

// Resources are the fields that name something outside this machine, by the
// path a user would look for in the profile.
func (p *Profile) Resources() map[string]string {
	named := map[string]string{
		"github.repo":     p.GitHub.Repo,
		"github.docsRepo": p.GitHub.DocsRepo,
	}

	if p.Docs != nil {
		named["docs.site"] = p.Docs.Site
	}

	if p.OBS != nil {
		named["obs.devProject"] = p.OBS.DevProject
		named["obs.stableProject"] = p.OBS.StableProject
	}

	if p.Screenshots != nil {
		named["screenshots.bucket"] = p.Screenshots.Bucket
	}

	if p.Responder != nil {
		named["responder.url"] = p.Responder.URL
	}

	resources := make(map[string]string, len(named))

	for field, value := range named {
		if value != "" {
			resources[field] = value
		}
	}

	return resources
}

// RefuseProductionResources fails when a profile that is not production names
// a resource production uses, naming the field. A fork profile that forgot to
// change stableProject cannot publish into the real one.
func (p *Profile) RefuseProductionResources(production *Profile) error {
	if p.Name == productionName {
		return nil
	}

	shared := production.Resources()

	for field, value := range p.Resources() {
		for productionField, productionValue := range shared {
			// GitHub ignores case in repository names. Folding case for the
			// other fields too refuses only names that differ from
			// production's by case.
			if strings.EqualFold(value, productionValue) {
				return fmt.Errorf("profile %q: %s is %q, which production uses as %s",
					p.Name, field, value, productionField)
			}
		}
	}

	return nil
}
