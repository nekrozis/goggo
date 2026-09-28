package core

import "fmt"

// UsageError is an argument failure the front end cannot judge while parsing.
// Whether --language applies, whether a DLC selector names anything, whether two
// selectors contradict each other: each answer needs the selected build's
// manifest, which has not been fetched yet.
//
// It travels back through the same channel as the parser's own refusals, so the
// process exits with the usage code: the argument was wrong, the command was
// not. Both the language resolution and the DLC selection report through it, so
// the class has exactly one definition rather than one per feature.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// Usagef builds a usage-class failure whose text the user can act on: it names
// the value that was refused and what the build does offer instead.
func Usagef(format string, args ...any) *UsageError {
	return &UsageError{msg: fmt.Sprintf(format, args...)}
}
