// Package release asks the Go module proxy whether a newer buildbox has been
// released, so init can say how to update.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	// Module is the path go install fetches buildbox from.
	Module = "github.com/hkluke/buildbox"

	// UpdateCommand installs the newest release.
	UpdateCommand = "go install " + Module + "@latest"

	// OptOut turns the check off: it tells the proxy that this address runs
	// buildbox.
	OptOut = "BUILDBOX_NO_UPDATE_CHECK"

	// DefaultProxy is the proxy go install uses unless GOPROXY says otherwise.
	DefaultProxy = "https://proxy.golang.org"

	checkTimeout = 3 * time.Second
	maxAnswer    = 64 << 10
)

var (
	errStatus = errors.New("module proxy answered")
	errEscape = errors.New("module path cannot be escaped")
)

// Disabled is whether the user turned the check off.
func Disabled() bool {
	return os.Getenv(OptOut) != ""
}

// Released is whether version is a tagged release. Builds from a checkout
// carry "dev" or a pseudo-version and are never compared.
func Released(version string) bool {
	return semver.IsValid(version) && semver.Prerelease(version) == "" && semver.Build(version) == ""
}

// Checker asks a module proxy for buildbox's newest release.
type Checker struct {
	Proxy  string
	Client *http.Client
	Agent  string
}

// Newer is the newest release when it is newer than current, or "" when
// there is nothing to update to. A development build is never checked.
func (checker Checker) Newer(ctx context.Context, current string) (string, error) {
	if !Released(current) {
		return "", nil
	}

	latest, err := checker.latest(ctx)
	if err != nil {
		return "", err
	}

	if !Released(latest) || semver.Compare(latest, current) <= 0 {
		return "", nil
	}

	return latest, nil
}

// latest is the version the proxy's @latest query names.
func (checker Checker) latest(ctx context.Context) (string, error) {
	escaped, err := module.EscapePath(Module)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errEscape, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, checker.Proxy+"/"+escaped+"/@latest", nil)
	if err != nil {
		return "", fmt.Errorf("could not build the module proxy request: %w", err)
	}

	request.Header.Set("User-Agent", checker.Agent)

	response, err := checker.Client.Do(request)
	if err != nil {
		return "", fmt.Errorf("could not reach the module proxy: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w %d", errStatus, response.StatusCode)
	}

	var answer struct {
		Version string `json:"Version"` //nolint:tagliatelle // The module proxy names the field Version.
	}

	err = json.NewDecoder(io.LimitReader(response.Body, maxAnswer)).Decode(&answer)
	if err != nil {
		return "", fmt.Errorf("could not read the module proxy's answer: %w", err)
	}

	return answer.Version, nil
}
