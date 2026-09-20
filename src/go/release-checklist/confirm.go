// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// confirmationsFile is the file holding the steps of one profile that
// somebody has marked done.
const confirmationsFile = "confirmations.yaml"

// Confirmation is one step somebody marked done, and the text they were
// looking at when they marked it.
type Confirmation struct {
	// At is when the step was marked done.
	At time.Time `yaml:"at"`
	// Digest identifies the text the mark covers. The step is available
	// again once that text changes, so notes edited after somebody approved
	// them need approving again.
	Digest string `yaml:"digest"`
}

// confirmations holds the steps whose check is a person's judgment. They are
// the only thing the tool stores about a release, since every other check
// reads the system that would show the step done.
type confirmations interface {
	// Confirmed is the mark on a step of a release, if it has one.
	Confirmed(version Version, step string) (Confirmation, bool)
	// Confirm records that somebody marked the step done for the text with
	// this digest.
	Confirm(version Version, step, digest string) error
	// Unconfirm takes the mark off a step.
	Unconfirm(version Version, step string) error
}

// confirmationStore keeps a profile's confirmations in the profile's own
// directory, so a rehearsal on a fork cannot mark a step of the real release
// done.
type confirmationStore struct {
	path string
	// marked is keyed by version and then by step, the order somebody
	// reading the file wants.
	marked map[string]map[string]Confirmation
}

// loadConfirmations reads a profile's confirmations.
func loadConfirmations(profile string) (*confirmationStore, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return nil, err
	}

	return confirmationsAt(filepath.Join(dir, confirmationsFile))
}

// confirmationsAt reads the confirmations kept in one file. A profile that
// has marked nothing has no file yet, so a missing file gives an empty
// store.
func confirmationsAt(path string) (*confirmationStore, error) {
	store := &confirmationStore{
		path:   path,
		marked: map[string]map[string]Confirmation{},
	}

	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}

	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, &store.marked); err != nil {
		return nil, fmt.Errorf("reading %s: %w", store.path, err)
	}

	if store.marked == nil {
		store.marked = map[string]map[string]Confirmation{}
	}

	return store, nil
}

func (s *confirmationStore) Confirmed(version Version, step string) (Confirmation, bool) {
	confirmation, marked := s.marked[version.String()][step]

	return confirmation, marked
}

func (s *confirmationStore) Confirm(version Version, step, digest string) error {
	steps, ok := s.marked[version.String()]
	if !ok {
		steps = map[string]Confirmation{}
		s.marked[version.String()] = steps
	}

	steps[step] = Confirmation{At: time.Now().UTC(), Digest: digest}

	return s.save()
}

func (s *confirmationStore) Unconfirm(version Version, step string) error {
	delete(s.marked[version.String()], step)

	if len(s.marked[version.String()]) == 0 {
		delete(s.marked, version.String())
	}

	return s.save()
}

func (s *confirmationStore) save() error {
	data, err := yaml.Marshal(s.marked)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o644)
}

// digestOf identifies the text somebody confirmed, so the tool notices a
// change to it without keeping a copy of the text.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}
