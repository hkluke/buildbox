package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// PathExpandFailed is the message for a path ExpandPath cannot resolve.
const PathExpandFailed = "Could not expand that path."

var (
	errEmptyPath = errors.New("empty path")
	errNoHome    = errors.New("could not find the home directory")
)

// CanonicalPath is how a path is stored: paths under the home directory
// become $HOME/..., which the reader expands again. The second result is a
// problem to show the user, or empty.
func CanonicalPath(path string) (string, string) {
	expanded, err := ExpandPath(path)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", PathExpandFailed
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return expanded, ""
	}

	if expanded == home {
		return "$HOME", ""
	}

	rest, found := strings.CutPrefix(expanded, home+string(os.PathSeparator))
	if found {
		return "$HOME/" + filepath.ToSlash(rest), ""
	}

	return expanded, ""
}

// Home is the user's home directory, or empty when it cannot be found.
func Home() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return home
}

// ExpandPath resolves ~, ~/, $HOME, and ${HOME}, and makes a relative path
// absolute.
func ExpandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errEmptyPath
	}

	if path == "~" || strings.HasPrefix(path, "~/") {
		home := Home()
		if home == "" {
			return "", errNoHome
		}

		if path == "~" {
			return home, nil
		}

		return filepath.Join(home, filepath.FromSlash(path[2:])), nil
	}

	home := Home()
	if home == "" {
		return path, nil
	}

	path = strings.ReplaceAll(path, "${HOME}", home)
	path = strings.ReplaceAll(path, "$HOME", home)

	if !filepath.IsAbs(path) {
		absolute, absErr := filepath.Abs(path)
		if absErr == nil {
			return absolute, nil
		}
	}

	return path, nil
}
