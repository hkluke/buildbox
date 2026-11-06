// Package abort holds the failures buildbox words for the user: a headline,
// optional detail such as the error an API returned, and an optional hint on
// what to do next.
package abort

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Indent starts a hint line that is a command to copy. Such a line is colored
// and never wrapped, since a split command would not run.
const Indent = "  "

// ErrAbort marks a failure that is already worded for the user.
var ErrAbort = errors.New("aborted")

// Error is a failure worded for the user. main prints it after the command
// returns, so it never lands on a line a running step still owns.
type Error struct {
	Headline string
	Detail   string
	Hint     string
}

// Error joins the parts that are set, one per line.
func (err *Error) Error() string {
	parts := []string{err.Headline}
	for _, part := range []string{err.Detail, err.Hint} {
		if part != "" {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, "\n")
}

// Unwrap lets errors.Is find ErrAbort.
func (err *Error) Unwrap() error {
	return ErrAbort
}

// New turns a message into a failure. The first line is the headline, and
// any further lines are detail, usually the error an API returned.
func New(message string) error {
	headline, detail, _ := strings.Cut(message, "\n")

	return &Error{Headline: strings.TrimSuffix(headline, "."), Detail: detail}
}

// Newf formats a message, then reads it like New.
func Newf(format string, args ...any) error {
	return New(fmt.Sprintf(format, args...))
}

// Hint builds a failure from its three parts.
func Hint(headline, detail, hint string) error {
	return &Error{Headline: headline, Detail: detail, Hint: hint}
}

// WithHint adds advice to a failure, such as how to clean up after it.
func WithHint(err error, hint string) error {
	var failed *Error
	if !errors.As(err, &failed) {
		return &Error{Headline: err.Error(), Hint: hint}
	}

	failed.Hint = strings.TrimSpace(failed.Hint + "\n" + hint)

	return err
}

// Usage points at the help for the misused command instead of printing all
// of it, so the mistake stays on screen.
func Usage(message, command string) error {
	topic := Prog() + " help"
	if command != "" {
		topic += " " + command
	}

	return Hint(strings.TrimSuffix(message, "."), "", "Run "+topic+" for usage.")
}

// Prog is the name to show in help and hints. go run builds into a temporary
// directory, so the full path of os.Args[0] would be noise.
func Prog() string {
	return filepath.Base(os.Args[0])
}
