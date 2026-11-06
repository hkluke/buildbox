package wizard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/console"
)

// A first run opens on a welcome rather than the overview: the logo, what
// init is about to do, and the checks as they come in, so the loading
// screen's hold is reading time. Enter begins the sections in order. Later
// runs keep the loading screen and open on the overview.

const (
	// textHeading heads every screen on a terminal too short for the
	// wordmark, and the loading screen and welcome too short for the logo.
	textHeading = "buildbox init"

	welcomeIntro = "Three sections, then a review. Nothing is written until you confirm."
	welcomeKeys  = "enter begin  ·  q quit"
	welcomeLabel = hubLabelWidth
	welcomeMark  = 2
	doneMark     = "✓ "
	failedMark   = "✗ "
	blankMark    = "  "
	readingText  = "reading locations, prices, and availability"

	// The plain welcome, for a terminal too short for the logo, sheds its
	// blank lines first, then the parts' descriptions, so it fits from
	// shortHeight rows up.
	welcomeTight    = 1
	welcomeTightest = 2
)

// welcomeRow is one labeled line of the welcome: a mark, a name in a
// column, and text after it.
type welcomeRow struct {
	mark string
	name string
	text string
	warn bool
}

// welcomeParts are the sections, in the order a first run meets them.
func (wiz Model) welcomeParts() []welcomeRow {
	tailnet := "policy lines and an OAuth client, from one access token"
	if wiz.tailnetSetUp() {
		tailnet = "done in an earlier run, skipped"
	}

	return []welcomeRow{
		{mark: blankMark, name: sectionName(sectionBox), text: "location, server type, and price"},
		{mark: blankMark, name: sectionName(sectionAgent), text: "Git key, Git identity, and coding tools"},
		{mark: blankMark, name: sectionName(sectionTailnet), text: tailnet},
	}
}

// welcomeChecks are what init found on this machine and at Hetzner: known
// at once for the tailnet, and as the catalog and the client check answer.
func (wiz Model) welcomeChecks() []welcomeRow {
	// The spinner's frames end in a space, so they take the mark's two
	// columns as the check does.
	done := wiz.paint.OK.Render(doneMark)
	wait := wiz.spin.View()

	tailscale := welcomeRow{mark: done, name: "Tailscale", text: joinDot(wiz.tailnet.Login, wiz.tailnetName(), "admin")}
	if !wiz.tailnet.Admin {
		tailscale.text = joinDot(wiz.tailnet.Login, wiz.tailnetName(), "not an admin")
		tailscale.warn = true
	}

	hetzner := welcomeRow{mark: wait, name: providerName, text: readingText}
	if wiz.loaded {
		hetzner = welcomeRow{
			mark: done,
			name: providerName,
			text: joinDot(plural(len(wiz.cat.Locations), "location"), plural(len(wiz.cat.Machines), "server type")),
		}
	}

	rows := []welcomeRow{tailscale, hetzner}
	if wiz.seedFound {
		rows = append(rows, wiz.seedCheck(done, wait))
	}

	return rows
}

// seedCheck is the line for an OAuth client left by an earlier run that
// quit before Write.
func (wiz Model) seedCheck(done, wait string) welcomeRow {
	name := sectionName(sectionTailnet)

	switch {
	case wiz.seedPending:
		return welcomeRow{mark: wait, name: name, text: "checking the OAuth client from an earlier run"}
	case wiz.tailnetSetUp():
		return welcomeRow{mark: done, name: name, text: "OAuth client from an earlier run, still valid"}
	default:
		return welcomeRow{
			mark: wiz.paint.Warn.Render(failedMark),
			name: name,
			text: "the earlier run's OAuth client was refused; a new one is made here",
			warn: true,
		}
	}
}

// tailnetSetUp reports whether the tailnet section has nothing left to do:
// an OAuth client that passed its check.
func (wiz Model) tailnetSetUp() bool {
	return wiz.tailnetDecided && wiz.MintsKeys()
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", count, noun)
}

// ready reports whether the welcome can begin: the catalog is in, the client
// check too, and the logo has had its minimum.
func (wiz Model) ready() bool {
	return wiz.loaded && !wiz.seedPending && wiz.drift >= minLoadTicks
}

// rowLines draws a welcome row, its text wrapped under itself.
func (wiz Model) rowLines(row welcomeRow, width int) []string {
	style := wiz.paint.Muted
	if row.warn {
		style = wiz.paint.Warn
	}

	room := max(width-welcomeMark-welcomeLabel, 1)
	parts := strings.Split(console.WrapText(row.text, room), "\n")
	lines := make([]string, 0, len(parts))

	for i, part := range parts {
		lead := strings.Repeat(" ", welcomeMark+welcomeLabel)
		if i == 0 {
			lead = row.mark + wiz.paint.Text.Render(console.PadRight(row.name, welcomeLabel))
		}

		lines = append(lines, lead+style.Render(part))
	}

	return lines
}

// welcomeBlock is the welcome's text under the logo: the plan, the checks,
// and any update notice, at the given tightness.
func (wiz Model) welcomeBlock(width, tight int) []string {
	lines := []string{}

	if tight < welcomeTightest {
		lines = append(lines, strings.Split(console.WrapText(welcomeIntro, width), "\n")...)
		lines = wiz.blankUnless(lines, tight)
	}

	for _, part := range wiz.welcomeParts() {
		if tight >= welcomeTightest {
			part.text = ""
		}

		lines = append(lines, wiz.rowLines(part, width)...)
	}

	lines = wiz.blankUnless(lines, tight)

	for _, check := range wiz.welcomeChecks() {
		lines = append(lines, wiz.rowLines(check, width)...)
	}

	if update := wiz.updateLine(); update != "" && tight < welcomeTight {
		lines = append(lines, update)
	}

	return lines
}

// blankUnless adds a blank line, unless the welcome is being tightened.
func (wiz Model) blankUnless(lines []string, tight int) []string {
	if tight >= welcomeTight {
		return lines
	}

	return append(lines, "")
}

func (wiz Model) welcomeFooter() string {
	if wiz.ready() {
		return wiz.paint.Muted.Render(welcomeKeys)
	}

	return wiz.paint.Muted.Render(quitHint)
}

// welcomeLines is the welcome with the logo: the block's lines are padded to
// one width, so it centers as a block under the centered logo.
func (wiz Model) welcomeLines(cube bool) []string {
	var lines []string

	if cube {
		lines = append(lines, cubeLines(wiz.drift)...)
		lines = append(lines, "")
	}

	lines = append(lines, wordmarkLines()...)
	lines = append(lines, "", wiz.paint.Muted.Render(bannerTagline), "")

	block := wiz.welcomeBlock(wiz.innerWidth(), 0)
	width := 0

	for _, line := range block {
		width = max(width, lipgloss.Width(line))
	}

	for _, line := range block {
		lines = append(lines, line+strings.Repeat(" ", width-lipgloss.Width(line)))
	}

	return append(lines, "", wiz.welcomeFooter())
}

// viewWelcome draws the welcome with the cube when the terminal has the
// rows, with the wordmark alone when it has fewer, and as plain text with
// the title when it has fewer still.
func (wiz Model) viewWelcome() string {
	if wiz.color && wiz.heroWidthFits() {
		if lines := wiz.welcomeLines(true); wiz.height > len(lines) {
			return wiz.placeLines(lines)
		}
	}

	if lines := wiz.welcomeLines(false); wiz.height > len(lines) {
		return wiz.placeLines(lines)
	}

	for tight := 0; ; tight++ {
		body := wiz.welcomePlain(tight)
		if strings.Count(body, "\n")+1+twoLines*screenPadY <= wiz.height || tight == welcomeTightest {
			return wiz.screen(body)
		}
	}
}

// welcomePlain is the welcome without the logo, under the title every
// screen falls back to.
func (wiz Model) welcomePlain(tight int) string {
	lines := []string{wiz.paint.Title.Render(textHeading), wiz.rule()}
	lines = wiz.blankUnless(lines, tight)
	lines = append(lines, wiz.welcomeBlock(wiz.innerWidth(), tight)...)
	lines = wiz.blankUnless(lines, tight)

	return strings.Join(append(lines, wiz.welcomeFooter()), "\n")
}

// heroWidthFits is whether the terminal is wide enough for the cube.
func (wiz Model) heroWidthFits() bool {
	heroWidth := heroBeside + len(cubeArt()[0]) + heroBeside

	return wiz.width >= heroWidth+twoLines*screenPadX
}

func (wiz Model) placeLines(lines []string) string {
	block := lipgloss.JoinVertical(lipgloss.Center, lines...)

	return lipgloss.Place(wiz.width, wiz.height, lipgloss.Center, lipgloss.Center, block)
}
