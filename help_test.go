package main

import (
	"testing"

	"github.com/hkluke/buildbox/internal/console"
)

func TestAliasesMapToTheirCommands(t *testing.T) {
	t.Parallel()

	for alias, want := range map[string]string{aliasList: cmdList, aliasDelete: cmdDelete, cmdCreate: cmdCreate} {
		if got := canonicalCommand(alias); got != want {
			t.Fatalf("canonicalCommand(%q) = %q, want %q", alias, got, want)
		}
	}

	topics := []string{
		cmdInit, cmdCreate, cmdList, cmdPrice, cmdForward, cmdDelete, cmdHelp, canonicalCommand(aliasDelete),
	}
	for _, name := range topics {
		if _, ok := commandHelp(name); !ok {
			t.Fatalf("help %s has no topic", name)
		}
	}
}

func TestVersionIsNeverEmpty(t *testing.T) {
	t.Parallel()

	if versionText() == "" {
		t.Fatal("empty version")
	}
}

func TestHelpStaysPlainInAPipe(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	text := rootHelp()
	if got := console.StyleHelp(text, console.StdoutPaint()); got != text {
		t.Fatalf("styled help changed the text:\n%s", got)
	}
}
