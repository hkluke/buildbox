// Package main is the buildbox command.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
)

const (
	flagHelp      = "--help"
	flagHelpShort = "-h"
	flagName      = "--name"
	flagNameEq    = "--name="
	flagVersion   = "--version"
	flagJSON      = "--json"
	flagForce     = "--force"
)

// exitInterrupted is the shell's code for a process ended by Ctrl-C.
const exitInterrupted = 130

func main() {
	// Ctrl-C and SIGTERM cancel the context instead of ending the process,
	// so the running step stops and create can report a server it already
	// made. The second signal ends the process at once, as before.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	go func() {
		<-ctx.Done()
		stop()
	}()

	err := run(ctx, os.Args[1:])
	if err == nil || errors.Is(err, errHelpShown) {
		return
	}

	console.PrintFailure(err)

	if ctx.Err() != nil {
		os.Exit(exitInterrupted)
	}

	os.Exit(1)
}

func newHetznerClient() (*hcloud.Client, error) {
	return hetzner.NewClient(buildVersion())
}

// consumeNameArg reads the next argument of a command that takes one server
// name, given either as NAME or as --name NAME. Naming it twice is an error,
// so a typo never silently picks one of the two.
func consumeNameArg(command, name string, args []string) (string, []string, error) {
	arg := args[0]
	if arg == flagHelp || arg == flagHelpShort {
		return "", nil, showCommandHelp(command)
	}

	if arg == flagName || strings.HasPrefix(arg, flagNameEq) {
		return takeNameFlag(command, name, args)
	}

	if strings.HasPrefix(arg, "-") {
		return "", nil, abort.Usage("Unknown argument: "+arg, command)
	}

	if name != "" {
		return "", nil, abort.Usage(command+" takes one name", command)
	}

	return arg, args[1:], nil
}

func takeNameFlag(command, name string, args []string) (string, []string, error) {
	if name != "" {
		return "", nil, abort.Usage(command+" takes one name", command)
	}

	if args[0] == flagName {
		if len(args) == 1 {
			return "", nil, abort.Usage(flagName+" needs a value", command)
		}

		return args[1], args[2:], nil
	}

	return strings.TrimPrefix(args[0], flagNameEq), args[1:], nil
}
