package abort

import (
	"errors"
	"testing"
)

var errPlain = errors.New("connection refused")

func TestNewSplitsHeadlineFromDetail(t *testing.T) {
	t.Parallel()

	var failed *Error
	if !errors.As(New("Could not read the firewall.\nAPI said no\nretry later"), &failed) {
		t.Fatal("not an abort.Error")
	}

	if failed.Headline != "Could not read the firewall" ||
		failed.Detail != "API said no\nretry later" || failed.Hint != "" {
		t.Fatalf("failure = %+v", failed)
	}

	one := Newf("Host %s is gone.", "dev")
	if !errors.As(one, &failed) || failed.Headline != "Host dev is gone" || failed.Detail != "" {
		t.Fatalf("one line = %+v", failed)
	}
}

func TestErrorJoinsOnlyTheSetParts(t *testing.T) {
	t.Parallel()

	if got := Hint("Invalid name", "", "Use lowercase letters.").Error(); got != "Invalid name\nUse lowercase letters." {
		t.Fatalf("got %q", got)
	}

	if !errors.Is(Hint("x", "", ""), ErrAbort) {
		t.Fatal("ErrAbort not found")
	}
}

func TestWithHintAppendsOrWraps(t *testing.T) {
	t.Parallel()

	worded := Hint("Server failed", "", "First hint.")
	if !errors.Is(WithHint(worded, "Second hint."), worded) {
		t.Fatal("an abort error was replaced")
	}

	var failed *Error
	if !errors.As(worded, &failed) || failed.Hint != "First hint.\nSecond hint." {
		t.Fatalf("appended = %+v", failed)
	}

	if !errors.As(WithHint(errPlain, "Try again."), &failed) {
		t.Fatal("a plain error was not wrapped")
	}

	if failed.Headline != errPlain.Error() || failed.Hint != "Try again." {
		t.Fatalf("wrapped = %+v", failed)
	}
}

func TestUsagePointsAtTheCommandHelp(t *testing.T) {
	t.Parallel()

	var failed *Error
	if !errors.As(Usage("Unknown argument: --bogus.", "list"), &failed) {
		t.Fatal("not an abort.Error")
	}

	if failed.Headline != "Unknown argument: --bogus" || failed.Hint != "Run "+Prog()+" help list for usage." {
		t.Fatalf("failure = %+v", failed)
	}

	if !errors.As(Usage("Unknown command: x", ""), &failed) || failed.Hint != "Run "+Prog()+" help for usage." {
		t.Fatalf("no command = %+v", failed)
	}
}
