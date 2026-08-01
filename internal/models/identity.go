package models

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrAliasConflict marks an alias that is already owned by another app.
	ErrAliasConflict = errors.New("application alias conflict")
	// ErrAmbiguousApplication marks a lookup that matched more than one app.
	ErrAmbiguousApplication = errors.New("ambiguous application reference")
)

// AliasConflictError is returned when registering an alias would silently
// redirect an existing reference to another application.
type AliasConflictError struct {
	Alias          string
	ExistingAppID  string
	RequestedAppID string
}

func (e *AliasConflictError) Error() string {
	return fmt.Sprintf("application alias %q already belongs to %q", e.Alias, e.ExistingAppID)
}

func (e *AliasConflictError) Unwrap() error { return ErrAliasConflict }

// AmbiguousApplicationError describes a reference that resolves to multiple
// application identities. Callers can use Matches to present a structured
// conflict instead of guessing.
type AmbiguousApplicationError struct {
	Reference string
	Matches   []string
}

func (e *AmbiguousApplicationError) Error() string {
	return fmt.Sprintf("application reference %q is ambiguous: %s", e.Reference, strings.Join(e.Matches, ", "))
}

func (e *AmbiguousApplicationError) Unwrap() error { return ErrAmbiguousApplication }
