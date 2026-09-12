package mfa

import "errors"

var (
	// ErrInvalidCode covers a wrong, malformed or expired code. Callers must render it
	// identically to "no factor enrolled", so the response never reveals who is worth
	// attacking.
	ErrInvalidCode = errors.New("invalid code")

	// ErrCodeReplayed means the code was correct but has already been used. It is
	// reported separately so it can be audited - a replay is a meaningful signal, not
	// just a typo - but it must reach the client as a plain invalid-code response.
	ErrCodeReplayed = errors.New("code has already been used")
)
