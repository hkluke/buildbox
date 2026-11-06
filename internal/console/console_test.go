package console

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/abort"
)

const testFirewall = "buildbox-tailscale-only"

func TestStepsInALogAreOneLineEach(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	con := NewWriter(&buf)

	err := con.Run("Firewall", "checking", func() (string, error) { return testFirewall, nil })
	if err != nil {
		t.Fatal(err)
	}

	con.Done("Auth key", "entered")
	con.Skip("known_hosts", "no entry for 100.64.0.2")

	failure := abort.New("Rootless Docker is not working.")

	err = con.Run("Docker", "checking", func() (string, error) { return "", failure })
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}

	want := "  ✓ Firewall     " + testFirewall + "\n" +
		"  ✓ Auth key     entered\n" +
		"  · known_hosts  no entry for 100.64.0.2\n" +
		"  ✗ Docker       failed\n" +
		"\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestFailureShowsDetailAndHint(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := abort.WithHint(
		abort.New("Could not read Hetzner firewall x.\nAPI said no"),
		"Delete it with:\n"+abort.Indent+"buildbox delete dev",
	)

	var failed *abort.Error
	if !errors.As(err, &failed) || !errors.Is(err, abort.ErrAbort) {
		t.Fatalf("err = %#v", err)
	}

	got := renderFailure(cliPaint(lipgloss.NewRenderer(&buf)), failed)
	want := "✗ Could not read Hetzner firewall x\n" +
		"    API said no\n" +
		"\n" +
		"  Delete it with:\n" +
		"    buildbox delete dev\n"

	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHintWrapsProseButNotCommands(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	paint := cliPaint(lipgloss.NewRenderer(&buf))
	command := abort.Indent + "ssh admin@100.64.0.2 sudo cat " + strings.Repeat("x", hintWidth)

	if lines := splitLines(hintLines(paint, command)); len(lines) != 1 {
		t.Fatalf("command wrapped:\n%s", strings.Join(lines, "\n"))
	}

	prose := strings.Repeat("word ", hintWidth/2)
	for _, line := range splitLines(hintLines(paint, prose)) {
		if lipgloss.Width(line) > hintWidth+len(StepIndent) {
			t.Fatalf("line too wide: %q", line)
		}
	}
}

func TestFailedIsAStepThatDidNotPass(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	NewWriter(&buf).Failed("Tailscale", "not running or not logged in")

	if want := "  ✗ Tailscale    not running or not logged in\n"; buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestJoinAndListsNaturally(t *testing.T) {
	t.Parallel()

	const item = "a terminal"

	for want, parts := range map[string][]string{
		"":                  nil,
		item:                {item},
		"A and B":           {"A", "B"},
		"A, B, and " + item: {"A", "B", item},
	} {
		if got := JoinAnd(parts); got != want {
			t.Errorf("JoinAnd(%q) = %q, want %q", parts, got, want)
		}
	}
}

func TestABlankHintLineSeparatesAdvice(t *testing.T) {
	t.Parallel()

	out := renderFailure(Paint{}, &abort.Error{Headline: "Missing", Hint: "First fix.\n\nSecond fix."})
	if !strings.Contains(out, "First fix.\n\n  Second fix.") {
		t.Fatalf("out = %q", out)
	}
}
