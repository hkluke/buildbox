package wizard

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/tailscale"
)

const clipboardTimeout = 3 * time.Second

// clipboardTools are tried in order: macOS, then Wayland, then X11.
func clipboardTools() [][]string {
	return [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
}

// CopyToClipboard reports whether a clipboard tool took the text. A failure
// is not an error: the caller shows the text either way.
func CopyToClipboard(ctx context.Context, text string) bool {
	for _, tool := range clipboardTools() {
		_, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}

		if runClipboard(ctx, tool, text) {
			return true
		}
	}

	return false
}

func runClipboard(ctx context.Context, tool []string, text string) bool {
	ctx, cancel := context.WithTimeout(ctx, clipboardTimeout)
	defer cancel()

	//nolint:gosec // G204: tool is one of the fixed clipboard commands above.
	cmd := exec.CommandContext(ctx, tool[0], tool[1:]...)
	cmd.Stdin = strings.NewReader(text)

	return cmd.Run() == nil
}

// browserOpened reports whether the desktop took the URL.
type browserOpened struct {
	url string
	err error
}

// stepPage is the admin console page a typing step asks you to visit, or "".
func (wiz Model) stepPage() string {
	switch wiz.step {
	case stepToken:
		return tailscale.KeysURL
	case stepOAuthID:
		return tailscale.OAuthURL
	case stepLoad, stepHub, stepLocation, stepMachine, stepImage, stepGitKey, stepGitIdentity, stepTools,
		stepAccess, stepPreview, stepPolicy, stepKeys, stepOAuthSecret:
		return ""
	default:
		return ""
	}
}

// openBrowser hands a URL to the desktop's opener. Bubble Tea runs it off
// the event loop, so a slow opener never freezes the form. The form has no
// context of its own; quitting it ends the process, opener and all.
func openBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}

		//nolint:gosec // G204: the opener is fixed and the URL is one of buildbox's constants.
		err := exec.CommandContext(context.Background(), opener, url).Start()

		return browserOpened{url: url, err: err}
	}
}

// copied reports whether the clipboard took the text, with the note to show
// either way.
type copied struct {
	ok     bool
	done   string
	failed string
}

func copyText(text, done, failed string) tea.Cmd {
	return func() tea.Msg {
		return copied{ok: CopyToClipboard(context.Background(), text), done: done, failed: failed}
	}
}
