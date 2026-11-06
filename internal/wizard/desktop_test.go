package wizard

import (
	"context"
	"testing"
)

func TestCopyToClipboardNeedsATool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if CopyToClipboard(context.Background(), "ssh-ed25519 AAAA test") {
		t.Fatal("copied without a clipboard tool")
	}
}
