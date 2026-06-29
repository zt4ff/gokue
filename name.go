package gokue

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidJobName is returned when a job name contains invalid characters or is empty.
	ErrInvalidJobName = errors.New("invalid job name")
	// ErrJobNameTooLong is returned when a job name exceeds the maximum length.
	ErrJobNameTooLong = errors.New("job name too long")
)

const maxJobNameLength = 255

// ValidateJobName checks that name is non-empty, within length limits,
// and contains only letters, digits, hyphens, underscores, dots, and spaces.
// Callers should trim whitespace before calling.
func ValidateJobName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: job name cannot be empty", ErrInvalidJobName)
	}
	if len(name) > maxJobNameLength {
		return fmt.Errorf("%w: job name must be at most %d characters (got %d)", ErrJobNameTooLong, maxJobNameLength, len(name))
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ' ') {
			return fmt.Errorf("%w: job name contains invalid character %q; only letters, digits, hyphens, underscores, dots, and spaces are allowed", ErrInvalidJobName, r)
		}
	}
	return nil
}
