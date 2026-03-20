// Package models defines the core domain data structures
// and shared domain errors used across the RentLoop application
//
// These errors represent common failure states returned by
// repositories. services and handlers
package models

import "errors"

var (
	ErrNotFound         = errors.New("record not found")
	ErrUnauthorised     = errors.New("unauthorised")
	ErrAccountSuspended = errors.New("account suspended - subscription payment required")
	ErrDuplicate        = errors.New("record already exists")
	ErrInvalidInput     = errors.New("invalid input")
	ErrNoMatch          = errors.New("no matching unit for account reference")
)
