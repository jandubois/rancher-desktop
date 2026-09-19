// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"strings"
)

// githubRepo is the repository the release is cut from. Every step that reads
// or writes it goes through gh, which keeps the credentials.
var githubRepo = &Resource{
	Name:       "GitHub repo",
	Command:    "gh",
	Install:    "https://cli.github.com",
	Configured: func(p *Profile) bool { return p.GitHub.Repo != "" },
	Probe: func(ctx context.Context, run *Run) (Answer, error) {
		repo := run.Profile.GitHub.Repo

		output, err := run.Tools.run(ctx, "gh", "api", "repos/"+repo, "--jq", ".permissions.push")
		if err != nil {
			return Answer{}, fmt.Errorf("reading your access to %s: %w", repo, err)
		}

		if strings.TrimSpace(string(output)) != "true" {
			return Answer{Detail: fmt.Sprintf(
				"no push access to %s; run gh auth login as a user who has it", repo)}, nil
		}

		return Answer{OK: true, Detail: "push access to " + repo}, nil
	},
}
