// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// runCheck is the CI gate. Bare `check` runs the locale-independent source
// gate (unused + undefined keys). With --locale it also runs the per-locale
// policy checks, where the policy defaults to the locale's manifest status
// (experimental or shipping) unless overridden by --policy. --locale=all
// runs the source gate once and then the policy checks for every translation
// locale.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	locale := fs.String("locale", "", "Target locale, or 'all' (omit for the source-only gate)")
	policy := fs.String("policy", "", "Override policy: experimental or shipping (requires --locale)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *policy != "" && *policy != string(StatusExperimental) && *policy != string(StatusShipping) {
		return fmt.Errorf("--policy must be experimental or shipping")
	}
	if *policy != "" && *locale == "" {
		return fmt.Errorf("--policy requires --locale")
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	// The source gate always runs. An operational failure (unreadable file)
	// aborts immediately; findings are collected and combined with the
	// per-locale results below.
	sourceErr := reportCheckSource(os.Stdout, root)
	if sourceErr != nil && !errors.Is(sourceErr, errFindings) {
		return sourceErr
	}

	if *locale == "" {
		return sourceErr
	}

	m, err := loadManifest(root)
	if err != nil {
		return err
	}

	var locales []ManifestLocale
	if *locale == "all" {
		locales = m.TranslationLocales()
	} else {
		entry, ok := m.Locales[*locale]
		if !ok {
			return fmt.Errorf("locale %q not found in meta/locales.yaml", *locale)
		}
		if entry.Status == StatusSource {
			return fmt.Errorf("locale %q is the source locale; per-locale checks do not apply", *locale)
		}
		locales = []ManifestLocale{{Code: *locale, Status: entry.Status}}
	}

	failed := sourceErr != nil
	for _, loc := range locales {
		pol := *policy
		if pol == "" {
			pol = string(loc.Status)
		}
		fmt.Println()
		if err := reportCheckPolicy(os.Stdout, root, loc.Code, pol); err != nil {
			if !errors.Is(err, errFindings) {
				return err
			}
			failed = true
		}
	}

	if failed {
		return findingsError("check failed")
	}
	return nil
}

// reportCheckSource runs the locale-independent source gate: keys defined in
// en-us.yaml but referenced nowhere (unused) and keys referenced in source
// but missing from en-us.yaml (undefined). Undefined keys render as "%key%"
// placeholders in every locale, so they fail regardless of locale.
func reportCheckSource(w io.Writer, root string) error {
	enPath := translationsPath(root, "en-us.yaml")
	enKeys, err := loadYAMLFlat(enPath)
	if err != nil {
		return err
	}

	refs, err := findKeyReferences(root, enKeys)
	if err != nil {
		return err
	}
	unusedCount := 0
	for k := range enKeys {
		if _, found := refs[k]; !found {
			unusedCount++
		}
	}

	undefined, err := findUndefinedKeys(root, enKeys)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "Source checks:")
	passed := true
	printResult := func(label string, count int) {
		status := "OK"
		if count > 0 {
			status = "FAIL"
			passed = false
		}
		fmt.Fprintf(w, "  %-30s %3d  %s\n", label+":", count, status)
	}

	printResult("unused keys", unusedCount)
	printResult("undefined keys", len(undefined))

	if passed {
		fmt.Fprintln(w, "Source checks passed.")
		return nil
	}
	return findingsError("source checks failed")
}

// reportCheckPolicy runs policy-aware checks on a single locale.
//
// Experimental policy:
//   - manifest valid
//   - locale file present
//   - no undefined keys
//   - no stale keys
//   - validate passes
//   - metadata coherent
//
// Shipping policy (all of the above plus):
//   - no missing keys
//   - no drifted keys
func reportCheckPolicy(w io.Writer, root, locale, policy string) error {
	passed := true
	printResult := func(label string, ok bool, detail string) {
		status := "OK"
		if !ok {
			status = "FAIL"
			passed = false
		}
		if detail != "" {
			fmt.Fprintf(w, "  %-35s %s  %s\n", label+":", status, detail)
		} else {
			fmt.Fprintf(w, "  %-35s %s\n", label+":", status)
		}
	}

	fmt.Fprintf(w, "Policy check (%s) for %s:\n", policy, locale)

	// Manifest valid and locale status matches policy.
	m, manifestErr := loadManifest(root)
	printResult("manifest valid", manifestErr == nil, errString(manifestErr))
	if manifestErr == nil {
		localeInfo, registered := m.Locales[locale]
		detail := ""
		if !registered {
			detail = "not found in meta/locales.yaml"
		}
		printResult("locale registered in manifest", registered, detail)
		if registered && policy == string(StatusShipping) {
			ok := localeInfo.Status == StatusShipping || localeInfo.Status == StatusSource
			detail = ""
			if !ok {
				detail = fmt.Sprintf("locale status is %q, shipping policy requires \"shipping\" or \"source\"", localeInfo.Status)
			}
			printResult("locale status matches policy", ok, detail)
		}
	}

	// Locale file present.
	localePath := translationsPath(root, locale+".yaml")
	enPath := translationsPath(root, "en-us.yaml")

	enKeys, err := loadYAMLFlat(enPath)
	if err != nil {
		return err
	}

	localeKeys, localeErr := loadYAMLFlat(localePath)
	printResult("locale file readable", localeErr == nil, errString(localeErr))
	if localeErr != nil {
		localeKeys = make(map[string]string)
	}

	// No undefined keys. Referenced-but-undefined keys render as "%key%"
	// placeholders in every locale, so they fail any policy.
	undefined, undefinedErr := findUndefinedKeys(root, enKeys)
	if undefinedErr != nil {
		return undefinedErr
	}
	printResult("no undefined keys", len(undefined) == 0, countDetail(len(undefined)))

	// No stale keys.
	staleCount := len(computeStale(enKeys, localeKeys))
	printResult("no stale keys", staleCount == 0, countDetail(staleCount))

	// Validate passes.
	validateErr := reportValidateQuiet(root, locale)
	printResult("validate passes", validateErr == nil, errString(validateErr))

	// Load metadata for the drift check below. Metadata coherence is already
	// covered by the "validate passes" check above (validateLocale checks it).
	meta, metaErr := loadMetadata(root, locale)
	if metaErr != nil {
		printResult("metadata readable", false, errString(metaErr))
	}

	// Shipping-only checks.
	if policy == "shipping" {
		missingCount := len(computeMissing(enKeys, localeKeys))
		printResult("no missing keys", missingCount == 0, countDetail(missingCount))

		if meta != nil {
			driftCount := len(computeDrifted(enKeys, meta, localeKeys))
			printResult("no drifted keys", driftCount == 0, countDetail(driftCount))
		}
	}

	if passed {
		fmt.Fprintf(w, "All %s policy checks passed for %s.\n", policy, locale)
		return nil
	}
	return findingsError(fmt.Sprintf("%s policy checks failed for %s", policy, locale))
}

// reportValidateQuiet runs validate and returns an error summary without
// printing individual errors.
func reportValidateQuiet(root, locale string) error {
	errs, err := validateLocale(root, locale)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d validation errors", len(errs))
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func countDetail(count int) string {
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d found", count)
}
