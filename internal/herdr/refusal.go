package herdr

import "errors"

// RefusedBeforeStart reports a local rejection before any mutating operation
// was attempted. Error codes alone cannot establish this: ErrNotGit and
// ErrTargetMissing can also follow a mutating RPC with an ambiguous outcome.
// This marker lets the broker return a reserved create unit safely.
func RefusedBeforeStart(err error) bool {
	var refusal *preflightRefusal
	return errors.As(err, &refusal)
}

type preflightRefusal struct{ cause error }

func (refusal *preflightRefusal) Error() string { return refusal.cause.Error() }
func (refusal *preflightRefusal) Unwrap() error { return refusal.cause }

func refused(err error) error { return &preflightRefusal{cause: err} }
