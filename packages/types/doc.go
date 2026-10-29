// Package types defines the shared domain model of Personal Cloud.
//
// It holds the core entities exchanged between the control plane
// (API, workspace controller, scheduler) and the data plane
// (compute nodes, runtime), with JSON tags for API serialization.
package types

import (
	"errors"
	"strings"
	"time"
)

// Common validation errors returned by constructors.
var (
	ErrEmptyID          = errors.New("types: empty id")
	ErrEmptyName        = errors.New("types: empty name")
	ErrEmptyUserID      = errors.New("types: empty user id")
	ErrEmptyWorkspaceID = errors.New("types: empty workspace id")
	ErrEmptyNodeID      = errors.New("types: empty node id")
)

// trimAndRequire trims s and returns an error if the result is empty.
func trimAndRequire(s string, err error) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", err
	}
	return s, nil
}

// nowUTC returns the current time in UTC, truncated to milliseconds so
// values survive a JSON round-trip without precision drift.
func nowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}
