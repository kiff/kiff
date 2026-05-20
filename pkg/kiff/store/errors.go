package store

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrMisconfigured = errors.New("store misconfigured")
)
