// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"regexp"
)

// packageVersion matches the version field of a package.json. A file with
// more than one is not one the tool will edit, because it cannot tell which
// field the release owns.
var packageVersion = regexp.MustCompile(`(?m)^(\s*"version":\s*)"([^"]*)"`)

// readPackageVersion is the version a package.json declares.
func readPackageVersion(content []byte) (Version, error) {
	matches := packageVersion.FindSubmatch(content)
	if matches == nil {
		return Version{}, fmt.Errorf("package.json has no version field")
	}

	return ParseVersion(string(matches[2]))
}

// setPackageVersion replaces the version in a package.json, leaving the rest
// of the file byte for byte, so the bump commit changes one line.
func setPackageVersion(content []byte, version Version) ([]byte, error) {
	found := packageVersion.FindAllSubmatchIndex(content, -1)
	if len(found) != 1 {
		return nil, fmt.Errorf("package.json has %d version fields, and the release owns one", len(found))
	}

	// The match runs from the start of the line to the closing quote, and
	// group 1 ends just before the opening quote, so the field's name and
	// spacing survive whatever they are.
	match := found[0]
	updated := make([]byte, 0, len(content))
	updated = append(updated, content[:match[3]]...)
	updated = append(updated, '"')
	updated = append(updated, version.String()...)
	updated = append(updated, '"')

	return append(updated, content[match[1]:]...), nil
}
