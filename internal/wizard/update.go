package wizard

import (
	"context"
	"net/http"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/release"
)

// updateFound is a release newer than the running build.
type updateFound struct {
	current string
	latest  string
}

// UpdateCheck asks the Go module proxy for a release newer than current. It
// stays silent when there is none or the proxy cannot be reached, so the
// check never gets in the way of init.
func UpdateCheck(ctx context.Context, current string) tea.Cmd {
	return func() tea.Msg {
		checker := release.Checker{Proxy: release.DefaultProxy, Client: http.DefaultClient, Agent: "buildbox/" + current}

		latest, err := checker.Newer(ctx, current)
		if err != nil || latest == "" {
			return nil
		}

		return updateFound{current: current, latest: latest}
	}
}

// WithUpdateCheck runs check alongside the catalog load.
func (wiz Model) WithUpdateCheck(check tea.Cmd) Model {
	wiz.updates = check

	return wiz
}

// updateLine is the notice of a newer release on the overview and on a
// first run's welcome, or "".
func (wiz Model) updateLine() string {
	shown := wiz.step == stepHub || (wiz.step == stepLoad && wiz.guided)
	if wiz.update.latest == "" || !shown {
		return ""
	}

	news := "buildbox " + wiz.update.latest + " is out"
	rest := " (you have " + wiz.update.current + "): "

	if lipgloss.Width(news+rest+release.UpdateCommand) > wiz.innerWidth() {
		return wiz.paint.Accent.Render(console.TrimWidth(news+rest+release.UpdateCommand, wiz.innerWidth()))
	}

	return wiz.paint.Accent.Render(news) + wiz.paint.Muted.Render(rest) + wiz.paint.Code.Render(release.UpdateCommand)
}

// hubExtraLines are the overview's lines beyond the ones hubChrome counts: a
// hint that wraps, the footer's note or spinner, and the update notice.
func (wiz Model) hubExtraLines() int {
	extra := wiz.hintOverflow()
	if wiz.checking || wiz.note != "" {
		extra++
	}

	if wiz.updateLine() != "" {
		extra++
	}

	return extra
}
