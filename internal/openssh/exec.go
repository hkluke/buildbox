package openssh

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hkluke/buildbox/internal/abort"
)

// streamWaitDelay is how long Stream gives a tool to end after an interrupt
// before it is killed.
const streamWaitDelay = time.Second

func scrubEnv(environ []string) []string {
	cleaned := make([]string, 0, len(environ))
	for _, entry := range environ {
		if strings.HasPrefix(entry, "TAILSCALE_API_KEY=") {
			continue
		}

		cleaned = append(cleaned, entry)
	}

	return cleaned
}

// Result is what a command printed.
type Result struct {
	Stdout string
	Stderr string
}

// Run runs one OpenSSH tool without a shell and without TAILSCALE_API_KEY in
// its environment.
func Run(ctx context.Context, name string, args ...string) (Result, error) {
	// name is ssh, scp, or ssh-keygen.
	// Arguments are a list, not a shell string.
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204,G702: executables are chosen by this program.
	cmd.Env = scrubEnv(os.Environ())

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return Result{Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("run %s: %w", name, err)
	}

	return Result{Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

// Stream runs one OpenSSH tool until it exits or ctx is canceled, passing
// each line it prints on stderr to report as it appears. It is for ssh
// sessions that stay up, where the lines matter while they happen. A
// canceled ctx interrupts the tool rather than killing it, so ssh closes
// its session instead of leaving the box to time it out.
func Stream(ctx context.Context, report func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204,G702: executables are chosen by this program.
	cmd.Env = scrubEnv(os.Environ())
	cmd.WaitDelay = streamWaitDelay
	cmd.Cancel = func() error {
		err := cmd.Process.Signal(os.Interrupt)
		if err != nil {
			return fmt.Errorf("interrupt %s: %w", name, err)
		}

		return nil
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		report(scanner.Text())
	}

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}

	return nil
}

// Require fails when one of the named commands is not installed.
func Require(names ...string) error {
	for _, name := range names {
		_, err := exec.LookPath(name)
		if err != nil {
			return abort.Hint(
				"Required command not found: "+name,
				"",
				"Install OpenSSH, which provides ssh, scp, and ssh-keygen.",
			)
		}
	}

	return nil
}

// PublicKeyOf derives the public key line of a private key file, the way
// ssh-keygen prints it. An encrypted key fails, since no passphrase is given.
func PublicKeyOf(ctx context.Context, privatePath string) (string, error) {
	result, err := Run(ctx, "ssh-keygen", "-y", "-P", "", "-f", privatePath)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(result.Stdout), nil
}

// keyFields is the key type and the key itself. A comment may follow them.
const keyFields = 2

// SameKey reports whether two public key lines carry the same key, whatever
// their comments say.
func SameKey(first, second string) bool {
	body := keyBody(first)

	return body != "" && body == keyBody(second)
}

func keyBody(line string) string {
	fields := strings.Fields(line)
	if len(fields) < keyFields {
		return ""
	}

	return fields[0] + " " + fields[1]
}
