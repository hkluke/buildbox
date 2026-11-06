package wizard

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/tailscale"
)

const (
	screenMargin      = 4
	narrowWidth       = 48
	shortHeight       = 16
	chromeLines       = 14
	machineChrome     = 18
	hubChrome         = 10
	minListLines      = 4
	screenPadX        = 2
	screenPadY        = 1
	minFieldDraw      = 24
	metaIndent        = 4
	pageSplit         = 2
	twoLines          = 2
	recommendedTag    = "(Recommended)"
	legacyRecommended = "recommended"
	toolChecked       = "[✓]"
	toolUnchecked     = "[ ]"
	policyStepTitle   = "Tailnet policy"
	accessStepTitle   = "Tailnet"
	previewStepTitle  = "Tailnet changes"
	presentMark       = "already present"
	previewFooter     = "space toggle  ·  enter apply  ·  esc back  ·  q quit"
	applyFooter       = "enter apply  ·  esc back  ·  q quit"
	keysStepTitle     = "Auth keys"
	gitStepTitle      = "Git key"
	customPathTitle   = "Use a key at a custom path"
	foundMeta         = "Already on this machine."
	configMeta        = "Already in the credentials file."
	continueFooter    = "enter continue  ·  esc back  ·  ctrl+c quit"
	pickFooter        = "↑↓ move  ·  enter select  ·  esc back  ·  q quit"
	policyFooter      = "o open the editor  ·  c copy the lines  ·  enter continue  ·  esc back  ·  q quit"
	pageFooter        = "ctrl+o open the page  ·  enter continue  ·  esc back  ·  ctrl+c quit"
	checkingNote      = "Checking the client with the Tailscale API"
	choiceBadgePad    = 5
	headerLines       = 5
)

// pickerRow is one line of a list, with an optional second line (meta) that
// wraps rather than being cut. warn draws the meta as a problem.
type pickerRow struct {
	header  bool
	gap     bool
	id      string
	title   string
	tag     string
	meta    string
	aside   string
	dim     bool
	warn    bool
	metaPad int
	facts   []hubFact
}

// skipped reports whether the cursor passes over the row.
func (row pickerRow) skipped() bool {
	return row.header
}

// spaced reports whether a blank line goes before the row, as it does
// before a group header and between hub rows.
func (row pickerRow) spaced() bool {
	return row.header || row.gap
}

// pad is the indent of the row's second line.
func (row pickerRow) pad() int {
	if row.metaPad > 0 {
		return row.metaPad
	}

	return metaIndent
}

// View draws the current step.
func (wiz Model) View() string {
	if wiz.width < narrowWidth || wiz.height < shortHeight {
		return "Terminal is too small for buildbox init.\n"
	}

	switch wiz.step {
	case stepLoad:
		return wiz.viewLoad()
	case stepPolicy:
		return wiz.viewPolicy()
	case stepPreview:
		return wiz.viewPreview()
	case stepGitIdentity:
		return wiz.viewIdentity()
	case stepLocation:
		return wiz.viewLocation()
	case stepHub, stepMachine, stepImage, stepGitKey, stepTools, stepAccess, stepToken, stepKeys, stepOAuthID,
		stepOAuthSecret:
		return wiz.viewForm()
	default:
		return ""
	}
}

func (wiz Model) viewForm() string {
	if wiz.typing() {
		return wiz.viewText()
	}

	return wiz.viewPick()
}

func (wiz Model) viewLoad() string {
	if wiz.guided {
		return wiz.viewWelcome()
	}

	if wiz.heroFits() {
		return wiz.viewHero()
	}

	body := strings.Join([]string{
		wiz.paint.Title.Render(textHeading),
		wiz.rule(),
		"",
		wiz.paint.Text.Render(wiz.spin.View() + "  " + wiz.loadingText()),
		"",
		wiz.paint.Muted.Render("ctrl+c quit"),
	}, "\n")

	return wiz.screen(body)
}

func (wiz Model) loadingText() string {
	text := "Reading locations, prices, and availability"
	if wiz.seedPending {
		text += ", checking the OAuth client"
	}

	return text
}

// viewPreview shows what applying the plan will do, line by line, before
// anything is written.
func (wiz Model) viewPreview() string {
	title, hint := wiz.question()
	lines := wiz.previewLines()

	parts := make([]string, 0, len(lines)+headerLines)
	parts = append(parts, wiz.viewHeader(title, hint))
	parts = append(parts, lines...)
	parts = append(parts, "", wiz.viewFooter())

	return wiz.screen(strings.Join(parts, "\n"))
}

func (wiz Model) previewLines() []string {
	width := wiz.innerWidth()
	lines := []string{}

	for _, change := range wiz.plan.Changes {
		if change.Present {
			lines = append(lines, wiz.paint.Muted.Render(console.TrimWidth("· "+change.Section+": "+presentMark, width)))

			continue
		}

		head := "+ " + change.Section
		if len(change.Adds) > 0 {
			head = "~ " + change.Section + ": adds " + strings.Join(change.Adds, ", ") + " to the rule"
		}

		lines = append(lines, wiz.paint.Text.Render(console.TrimWidth(head, width)))

		for wrapped := range strings.SplitSeq(console.WrapText(change.Line, width-screenPadX), "\n") {
			lines = append(lines, wiz.paint.Code.Render("    "+wrapped))
		}
	}

	if wiz.plan.StockRule != tailscale.NoStockRule {
		box := toolUnchecked
		if wiz.narrow {
			box = toolChecked
		}

		lines = append(lines,
			wiz.paint.Text.Render(console.TrimWidth(box+` Change the allow-all rule's "src": ["*"] to autogroup:member`, width)),
		)

		why := "Without it, boxes reach every device on the tailnet. Your other tagged devices lose that reach as well."
		for wrapped := range strings.SplitSeq(console.WrapText(why, width-metaIndent), "\n") {
			lines = append(lines, wiz.paint.Muted.Render(strings.Repeat(" ", metaIndent)+wrapped))
		}
	}

	client := "+ OAuth client for " + wiz.tag + ", with the auth_keys scope"
	if wiz.MintsKeys() {
		client = "· OAuth client: from the credentials file, checked again."
	}

	return append(lines, wiz.paint.Text.Render(console.TrimWidth(client, width)))
}

// viewPolicy shows the lines to paste into the tailnet policy, one per
// section, with the keys that open the editor or copy them.
func (wiz Model) viewPolicy() string {
	title, hint := wiz.question()
	lines := wiz.policyLines()

	parts := make([]string, 0, len(lines)+headerLines)
	parts = append(parts, wiz.viewHeader(title, hint))
	parts = append(parts, lines...)
	parts = append(parts, "", wiz.viewFooter())

	return wiz.screen(strings.Join(parts, "\n"))
}

func (wiz Model) policyLines() []string {
	width := wiz.innerWidth()
	lines := []string{}

	for _, block := range tailscale.PolicyBlocks(wiz.tag) {
		lines = append(lines, wiz.paint.Muted.Render(block.Section))

		for wrapped := range strings.SplitSeq(console.WrapText(block.Line, width-screenPadX), "\n") {
			lines = append(lines, wiz.paint.Code.Render("  "+wrapped))
		}
	}

	return lines
}

// policyText is what c copies: the same lines, each under a comment naming
// its section.
func (wiz Model) policyText() string {
	var out strings.Builder

	for _, block := range tailscale.PolicyBlocks(wiz.tag) {
		out.WriteString("// " + block.Section + "\n" + block.Line + "\n")
	}

	return out.String()
}

func (wiz Model) viewText() string {
	title, hint := wiz.question()
	field := wiz.input.View()

	width := min(max(lipgloss.Width(field), minFieldDraw), wiz.innerWidth())

	parts := []string{
		wiz.viewHeader(title, hint),
		field,
		wiz.paint.Rule.Render(strings.Repeat("─", width)),
	}
	if live := wiz.liveNote(); live != "" {
		parts = append(parts, live)
	}

	parts = append(parts, "", wiz.viewFooter())

	return wiz.screen(strings.Join(parts, "\n"))
}

func (wiz Model) viewPick() string {
	title, hint := wiz.question()
	parts := []string{wiz.viewHeader(title, hint)}

	if wiz.step == stepMachine {
		parts = append(parts, wiz.paint.Muted.Render(wiz.filterLine()), "")
	}

	rows := wiz.rows()
	if len(rows) == 0 {
		parts = append(parts, wiz.paint.Warn.Render(wiz.emptyList()))
	} else {
		parts = append(parts, wiz.viewRows(rows))
	}

	if pin := wiz.viewPin(); pin != "" {
		parts = append(parts, "", pin)
	}

	parts = append(parts, "", wiz.viewFooter())

	return wiz.screen(strings.Join(parts, "\n"))
}

func (wiz Model) viewHeader(title, hint string) string {
	width := wiz.innerWidth()

	right := wiz.paint.Muted.Render(wiz.progress())
	if wiz.step == stepHub {
		right = wiz.paint.Muted.Render(wiz.hubPlace())
	}

	// A first run shows its whole journey beside the title when there is
	// room, and the section's own count otherwise.
	left := lipgloss.Width(textHeading)
	if wiz.headerBanner() {
		left = wordmarkWidth
	}

	if rail := wiz.journey(); rail != "" && lipgloss.Width(rail) <= width-left-1 {
		right = rail
	}

	top := []string{placeRight(width, wiz.paint.Title.Render(textHeading), right)}
	if wiz.headerBanner() {
		// A narrow terminal shortens the text beside the wordmark, never the
		// wordmark itself.
		right = console.TrimWidth(right, width-wordmarkWidth-1)
		top = wordmarkLines()
		top[len(top)-1] = placeRight(width, top[len(top)-1], right)
	}

	hintLines := wiz.hintLines(hint, width)
	lines := make([]string, 0, len(top)+len(hintLines)+headerLines)
	lines = append(lines, top...)
	lines = append(
		lines,
		wiz.rule(),
		"",
		wiz.paint.Title.Render(console.TrimWidth(title, width)),
	)
	lines = append(lines, hintLines...)
	lines = append(lines, "")

	return strings.Join(lines, "\n")
}

// hintStyle is plain text where the hint is the question to answer, and
// muted where it explains a field.
func (wiz Model) hintStyle() lipgloss.Style {
	if (wiz.step == stepGitKey && !wiz.gitEditing) || wiz.step == stepPolicy || wiz.step == stepKeys ||
		wiz.step == stepAccess || wiz.step == stepPreview || wiz.step == stepHub {
		return wiz.paint.Text
	}

	return wiz.paint.Muted
}

func (wiz Model) hintLines(hint string, width int) []string {
	if hint == "" {
		return nil
	}

	// A hint's own line breaks stay; each line wraps on its own, and a blank
	// one separates steps from the explanation.
	var wrapped []string

	for part := range strings.SplitSeq(hint, "\n") {
		if strings.TrimSpace(part) == "" {
			wrapped = append(wrapped, "")

			continue
		}

		wrapped = append(wrapped, wrapHintLine(part, width)...)
	}

	return paintHint(wrapped, hintLinks(hint), wiz.hintStyle(), wiz.paint.Title)
}

func (wiz Model) viewFooter() string {
	text := wiz.footerText()

	lines := []string{}

	switch {
	case wiz.checking:
		lines = append(lines, wiz.paint.Text.Render(wiz.spin.View()+" "+wiz.busy))
	case wiz.note != "":
		lines = append(lines, wiz.paint.Warn.Render(console.TrimWidth(wiz.note, wiz.innerWidth())))
	}

	if update := wiz.updateLine(); update != "" {
		lines = append(lines, update)
	}

	keys := wiz.paint.Muted.Render(console.TrimWidth(text, wiz.innerWidth()))
	if wiz.askingQuit {
		keys = wiz.paint.Warn.Render(console.TrimWidth(quitQuestion, wiz.innerWidth()))
	}

	lines = append(lines, keys)

	return strings.Join(lines, "\n")
}

func (wiz Model) viewRows(rows []pickerRow) string {
	start, end := windowRows(rows, wiz.selectedID(), wiz.listBudget(), wiz.innerWidth())
	parts := make([]string, 0, end-start)

	for i := start; i < end; i++ {
		if rows[i].spaced() && i > start {
			parts = append(parts, "")
		}

		parts = append(parts, wiz.viewRow(rows[i]))
	}

	return strings.Join(parts, "\n")
}

func (wiz Model) viewRow(row pickerRow) string {
	width := wiz.innerWidth()
	if row.header {
		return wiz.paint.Faint.Render(console.TrimWidth(row.title, width))
	}

	if len(row.facts) > 0 {
		return wiz.viewFactRow(row)
	}

	mark := "  "
	if row.id == wiz.selectedID() {
		mark = "▸ "
	}

	label := row.title
	if row.tag != "" {
		label += "  " + row.tag
	}

	line := mark + placeRight(width-lipgloss.Width(mark), label, row.aside)
	style := wiz.rowStyle(row)
	out := style.Render(console.TrimWidth(line, width))

	if row.meta == "" {
		return out
	}

	return strings.Join(append([]string{out}, wiz.viewMeta(row)...), "\n")
}

// viewMeta is the row's second line, wrapped and indented, in the shade of
// a note, a problem, or a sold-out row.
func (wiz Model) viewMeta(row pickerRow) []string {
	style := wiz.paint.Muted

	switch {
	case row.warn:
		style = wiz.paint.Warn
	case row.dim && row.id != wiz.selectedID():
		style = wiz.paint.Faint
	}

	parts := metaLines(row, wiz.innerWidth())
	lines := make([]string, 0, len(parts))

	for _, part := range parts {
		lines = append(lines, style.Render(strings.Repeat(" ", row.pad())+part))
	}

	return lines
}

// metaLines wraps a row's second line to the width left after its indent,
// so an explanation is read in full rather than cut.
func metaLines(row pickerRow, width int) []string {
	if row.meta == "" {
		return nil
	}

	return strings.Split(console.WrapText(row.meta, max(width-row.pad(), 1)), "\n")
}

func (wiz Model) rowStyle(row pickerRow) lipgloss.Style {
	switch {
	case row.id == wiz.selectedID() && row.dim:
		return wiz.paint.Warn
	case row.id == wiz.selectedID():
		return wiz.paint.Accent
	case row.dim:
		return wiz.paint.Faint
	default:
		return wiz.paint.Text
	}
}

func (wiz Model) footerText() string {
	switch wiz.step {
	case stepHub:
		return wiz.hubFooter()
	case stepGitIdentity:
		return wiz.identityFooter()
	case stepPreview:
		return wiz.previewFooterText()
	case stepGitKey:
		return choiceFooter(wiz.gitEditing)
	case stepLoad, stepLocation, stepMachine, stepImage, stepTools, stepAccess, stepToken, stepPolicy,
		stepKeys, stepOAuthID, stepOAuthSecret:
		return fixedFooter(wiz.step)
	default:
		return continueFooter
	}
}

// fixedFooter is the footer of a step whose keys never change.
func fixedFooter(step stepID) string {
	footers := map[stepID]string{
		stepMachine:  "↑↓ move  ·  a arch  ·  c cpu  ·  enter select  ·  esc back  ·  q quit",
		stepImage:    pickFooter,
		stepLocation: locationKeys,
		stepKeys:     pickFooter,
		stepAccess:   pickFooter,
		stepTools:    "space toggle  ·  ↑↓ move  ·  enter select  ·  esc back  ·  q quit",
		stepPolicy:   policyFooter,
		stepToken:    pageFooter,
		stepOAuthID:  pageFooter,
	}

	if text, ok := footers[step]; ok {
		return text
	}

	return continueFooter
}

// previewFooterText offers the toggle only while there is a rule to narrow
// and nothing has been written yet.
func (wiz Model) previewFooterText() string {
	if wiz.plan.StockRule != tailscale.NoStockRule && !wiz.setUp {
		return previewFooter
	}

	return applyFooter
}

func (wiz Model) viewPin() string {
	body := wiz.pinBody()
	if body == "" {
		return ""
	}

	return wiz.rule() + "\n" + body
}

func (wiz Model) pinBody() string {
	switch wiz.step {
	case stepLocation:
		return wiz.pinLocation()
	case stepMachine:
		return wiz.pinMachine()
	case stepImage:
		return wiz.pinImage()
	case stepLoad, stepHub, stepAccess, stepToken, stepPreview, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret,
		stepGitKey, stepGitIdentity, stepTools:
		return ""
	default:
		return ""
	}
}

func (wiz Model) pinLocation() string {
	location, ok := wiz.cat.Location(wiz.location)
	if !ok {
		return ""
	}

	width := wiz.innerWidth()

	return strings.Join([]string{
		wiz.paint.Accent.Render(console.TrimWidth(locationLabel(location), width)),
		wiz.paint.Text.Render(console.TrimWidth(location.Name+"  ·  "+location.NetworkZone, width)),
		wiz.paint.OK.Render(console.TrimWidth(locationState(location), width)),
	}, "\n")
}

func (wiz Model) pinMachine() string {
	machine, place, ok := wiz.cat.Place(wiz.serverType, wiz.location)
	if !ok {
		return wiz.paint.Muted.Render("No server type selected.")
	}

	width := wiz.innerWidth()

	title := machine.Name
	if mark := machineTag(place); mark != "" && mark != legacyRecommended {
		title += "   " + mark
	}

	specs := fmt.Sprintf(
		"%d vCPU    %s memory    %d GB %s disk    %s",
		machine.Cores,
		hetzner.MemoryLabel(machine.Memory),
		machine.Disk,
		storageLabel(machine.Storage),
		machine.Arch,
	)
	price := priceSentence(wiz.cat.Currency, place)

	lines := []string{
		wiz.paint.Accent.Render(console.TrimWidth(title, width)),
		wiz.paint.Text.Render(console.TrimWidth(specs, width)),
		wiz.paint.Text.Render(console.TrimWidth(price, width)),
	}
	if traffic := formatTraffic(place.Included); traffic != "" {
		lines = append(lines, wiz.paint.Muted.Render(console.TrimWidth(traffic, width)))
	}

	return strings.Join(lines, "\n")
}

func (wiz Model) pinImage() string {
	for _, image := range wiz.cat.Images {
		if image.Name == wiz.image {
			return wiz.paint.Text.Render(console.TrimWidth(image.Name+"  ·  "+image.Detail, wiz.innerWidth()))
		}
	}

	return ""
}

func (wiz Model) tailnetName() string {
	if wiz.tailnet.Name != "" {
		return wiz.tailnet.Name
	}

	return "this tailnet"
}

func (wiz Model) question() (string, string) {
	if wiz.step == stepMachine {
		return "Server type", "Billed by the hour while the box exists, up to the monthly cap. " +
			"Gross, VAT included. Dim rows are sold out in " + wiz.placeName() + "."
	}

	if wiz.step == stepPolicy {
		return wiz.policyQuestion()
	}

	if wiz.step == stepGitKey {
		return wiz.gitQuestion()
	}

	if pair, ok := wiz.tailnetQuestions()[wiz.step]; ok {
		return pair[0], pair[1]
	}

	questions := map[stepID][2]string{
		stepHub: wiz.hubQuestion(),
		stepGitIdentity: {
			identityStepTitle,
			"The name and email on the agent's commits, its git user.name and user.email.",
		},
		stepTools: {
			"Coding tools",
			"Installed on the box for the agent, beside Git and Docker.",
		},
		stepImage:    {"Image", "The Ubuntu release the box boots."},
		stepLocation: {"Location", "Where the box runs. Prices on the next screen are for this location."},
	}

	pair, ok := questions[wiz.step]
	if !ok {
		return "", ""
	}

	return pair[0], pair[1]
}

// tailnetQuestions are the titles and hints of the tailnet steps, which all
// name the tag.
func (wiz Model) tailnetQuestions() map[stepID][2]string {
	return map[stepID][2]string{
		stepAccess: {accessStepTitle, wiz.accessHint()},
		stepToken: {
			"Access token",
			"1. Open " + tailscale.KeysURL + "\n" +
				"2. Click “Generate access token” and set the expiry to 1 day.\n" +
				"3. Paste the token here.\n" +
				"\n" +
				"The token is used once, for this setup, and never stored.",
		},
		stepPreview: {
			previewStepTitle,
			"Enter applies these changes to " + wiz.tailnetName() + "'s policy and creates an OAuth client for " +
				wiz.tag + ". Until then, nothing is written.",
		},
		stepKeys: {
			keysStepTitle,
			"Each box joins with a one-time auth key for " + wiz.tag + ". create can mint it through an OAuth " +
				"client, or ask you to paste one.",
		},
		stepOAuthID: {
			"OAuth client",
			"1. Open " + tailscale.OAuthURL + "\n" +
				"2. Click “Generate OAuth client”, tick write for auth_keys, and add the tag " + wiz.tag + ".\n" +
				"3. Paste the client id here.\n" +
				"\n" +
				"The secret is asked next.",
		},
		stepOAuthSecret: {
			"OAuth client secret",
			"Shown once, when the client is created. It stays in the credentials file beside the config, and " +
				"can only mint one-time keys for " + wiz.tag + ".",
		},
	}
}

// accessHint explains the tailnet section. Once the tailnet is set up, a
// second visit reads as a repair rather than a first setup.
func (wiz Model) accessHint() string {
	if wiz.tailnetDecided && wiz.MintsKeys() {
		return wiz.tailnetName() + " is set up: the policy has the lines for " + wiz.tag + " and the OAuth " +
			"client works. Continue to repair the policy or replace the client; esc keeps it as is."
	}

	return "Boxes join " + wiz.tailnetName() + " tagged " + wiz.tag + ", which takes three policy lines and " +
		"an OAuth client to mint their auth keys. Set up both with a one-time access token, or by hand."
}

func (wiz Model) placeName() string {
	location, ok := wiz.cat.Location(wiz.location)
	if !ok {
		return wiz.location
	}

	return locationLabel(location)
}

func (wiz Model) rows() []pickerRow {
	switch wiz.step {
	case stepHub:
		return wiz.hubRows()
	case stepAccess:
		return wiz.accessRows()
	case stepKeys:
		return wiz.keysRows()
	case stepGitKey:
		return wiz.gitRows()
	case stepTools:
		return wiz.toolRows()
	case stepImage, stepLocation, stepMachine:
		return wiz.catalogRows()
	case stepLoad, stepToken, stepPreview, stepPolicy, stepOAuthID, stepOAuthSecret, stepGitIdentity:
		return nil
	default:
		return nil
	}
}

// catalogRows are the Box section's lists, built from the Hetzner catalog.
func (wiz Model) catalogRows() []pickerRow {
	if wiz.step == stepImage {
		return imageRows(wiz.cat.Images, wiz.suggestImage)
	}

	if wiz.step == stepLocation {
		return locationRows(wiz.cat.Locations, wiz.suggestLocation)
	}

	return wiz.machineRows()
}

func (wiz Model) toolRows() []pickerRow {
	selected := config.ToolSet(wiz.tools)
	rows := make([]pickerRow, 0, len(config.Tools()))

	for _, tool := range config.Tools() {
		box := toolUnchecked
		if selected[tool.ID] {
			box = toolChecked
		}

		rows = append(rows, pickerRow{
			id:    tool.ID,
			title: box + " " + tool.Name,
			tag:   withRecommended("", tool.ID == config.ToolOpenCode),
		})
	}

	return rows
}

func imageRows(offers []hetzner.ImageOffer, suggest string) []pickerRow {
	rows := make([]pickerRow, 0, len(offers))

	for _, offer := range offers {
		rows = append(rows, pickerRow{
			id:    offer.Name,
			title: offer.Name,
			tag:   withRecommended("", offer.Name == suggest),
			meta:  offer.Detail,
		})
	}

	return rows
}

func locationRows(offers []hetzner.LocationOffer, suggest string) []pickerRow {
	rows := make([]pickerRow, 0, len(offers))

	for _, offer := range offers {
		rows = append(rows, pickerRow{
			id:    offer.Name,
			title: offer.Name,
			tag:   withRecommended(locationLabel(offer), offer.Name == suggest),
			meta:  joinDot(offer.NetworkZone, locationState(offer)),
			dim:   offer.Available == 0,
		})
	}

	return rows
}

type machineChoice struct {
	machine hetzner.MachineOffer
	place   hetzner.PlaceOffer
}

func (wiz Model) machineRows() []pickerRow {
	available, unavailable := wiz.splitMachines()
	sortMachines(available)
	sortMachines(unavailable)

	rows := groupedMachines(wiz.cat.Currency, available, wiz.suggestMachine)
	if len(unavailable) == 0 {
		return rows
	}

	rows = append(rows, pickerRow{header: true, title: "Unavailable"})
	for _, choice := range unavailable {
		rows = append(rows, machineRow(wiz.cat.Currency, choice, true, wiz.suggestMachine))
	}

	return rows
}

func (wiz Model) splitMachines() ([]machineChoice, []machineChoice) {
	var available, unavailable []machineChoice

	for _, machine := range wiz.cat.Machines {
		if wiz.archFilter != "" && machine.Arch != wiz.archFilter {
			continue
		}

		if wiz.cpuFilter != "" && machine.CPU != wiz.cpuFilter {
			continue
		}

		place, ok := machine.Places[wiz.location]
		if !ok {
			continue
		}

		choice := machineChoice{machine: machine, place: place}
		if place.Creatable {
			available = append(available, choice)
		} else {
			unavailable = append(unavailable, choice)
		}
	}

	return available, unavailable
}

func sortMachines(choices []machineChoice) {
	slices.SortStableFunc(choices, func(left, right machineChoice) int {
		if left.machine.Category != right.machine.Category {
			return strings.Compare(left.machine.Category, right.machine.Category)
		}

		if hetzner.MonthlyValue(left.place.MonthlyGross) != hetzner.MonthlyValue(right.place.MonthlyGross) {
			if hetzner.MonthlyValue(left.place.MonthlyGross) < hetzner.MonthlyValue(right.place.MonthlyGross) {
				return -1
			}

			return 1
		}

		return strings.Compare(left.machine.Name, right.machine.Name)
	})
}

func groupedMachines(currency string, choices []machineChoice, suggest string) []pickerRow {
	rows := []pickerRow{}
	last := ""

	for _, choice := range choices {
		category := labelize(choice.machine.Category)
		if category != last {
			rows = append(rows, pickerRow{header: true, title: category})
			last = category
		}

		rows = append(rows, machineRow(currency, choice, false, suggest))
	}

	return rows
}

func machineRow(currency string, choice machineChoice, dim bool, suggest string) pickerRow {
	place := choice.place

	meta := joinDot(
		fmt.Sprintf("%d vCPU", choice.machine.Cores),
		hetzner.MemoryLabel(choice.machine.Memory),
		fmt.Sprintf("%d GB %s", choice.machine.Disk, storageLabel(choice.machine.Storage)),
		choice.machine.Arch,
	)
	if traffic := formatTraffic(place.Included); traffic != "" {
		meta = joinDot(meta, traffic)
	}

	return pickerRow{
		id:    choice.machine.Name,
		title: choice.machine.Name,
		tag:   choiceTag(machineTag(place), !dim && choice.machine.Name == suggest),
		meta:  meta,
		aside: priceLabel(currency, place),
		dim:   dim,
	}
}

func machineTag(place hetzner.PlaceOffer) string {
	if !place.Creatable {
		return "unavailable"
	}

	if place.Deprecated {
		return "deprecated"
	}

	if place.Recommended {
		return legacyRecommended
	}

	return ""
}

func choiceTag(tag string, chosen bool) string {
	if tag == legacyRecommended {
		tag = ""
	}

	return withRecommended(tag, chosen)
}

func withRecommended(tag string, chosen bool) string {
	if !chosen {
		return tag
	}

	if tag == "" {
		return recommendedTag
	}

	return tag + "  " + recommendedTag
}

func (wiz Model) policyQuestion() (string, string) {
	hint := "Add these to your tailnet policy, one line per section. They make boxes the tag's " +
		"devices rather than yours, let your devices reach them, and let you log in as " + config.AgentUser +
		" or " + config.AdminUser + `. Also change the default acls rule's "src": ["*"] to ` +
		"autogroup:member, or boxes reach every device on the tailnet."
	if !wiz.tailnet.Admin {
		hint = "You are not an admin of " + wiz.tailnetName() + ", so an admin has to add these. " + hint
	}

	return policyStepTitle, hint
}

// choiceFooter is for the Git key step: a list, or the path editor.
func choiceFooter(editing bool) string {
	if editing {
		return continueFooter
	}

	return pickFooter
}

func (wiz Model) accessRows() []pickerRow {
	token := pickerRow{
		id:    accessToken,
		title: "Let buildbox set up the tailnet",
		meta: "Paste an access token. buildbox adds the policy lines, creates the OAuth client, and " +
			"discards the token.",
	}
	manual := pickerRow{
		id:    accessManual,
		title: "Set up the tailnet by hand",
		meta: "buildbox shows the lines to paste. You create the OAuth client yourself, or paste an auth key " +
			"at every create.",
	}

	return []pickerRow{wiz.markRecommended(token, true), manual}
}

func (wiz Model) keysRows() []pickerRow {
	mint := pickerRow{
		id:    keysChoiceMint,
		title: "Let buildbox mint auth keys",
		meta:  "An OAuth client with the auth_keys scope for " + wiz.tag + ". Nothing to paste at create.",
	}
	if wiz.MintsKeys() {
		mint.meta = configMeta
	}

	ask := pickerRow{
		id:    keysChoiceAsk,
		title: "Paste a one-time key for each box",
		meta:  "Every create shows where to get one and which options to pick.",
	}

	return []pickerRow{wiz.markRecommended(mint, true), ask}
}

func (wiz Model) gitQuestion() (string, string) {
	if wiz.gitEditing {
		return gitStepTitle, "Path to the private key. Its public half must already be on GitHub or GitLab."
	}

	return gitStepTitle, "The agent's SSH key for Git, copied to the box. Give it a key of its own, not yours. " +
		"A generated key's public half is shown at the end, to add to GitHub or GitLab."
}

func (wiz Model) gitRows() []pickerRow {
	custom := pickerRow{
		id:    gitChoicePath,
		title: customPathTitle,
		meta:  "Type the private key path.",
	}
	if wiz.gitSuggest == gitChoiceUse {
		return []pickerRow{wiz.gitUseRow(), wiz.gitNewRow(), custom}
	}

	return []pickerRow{wiz.gitNewRow(), custom}
}

func (wiz Model) gitUseRow() pickerRow {
	row := pickerRow{
		id:    gitChoiceUse,
		title: "Use " + console.DisplayPath(wiz.gitKey),
		meta:  foundMeta,
	}

	return wiz.markRecommended(row, wiz.gitSuggest == gitChoiceUse)
}

func (wiz Model) gitNewRow() pickerRow {
	row := pickerRow{
		id:    gitChoiceNew,
		title: "Generate a dedicated key",
		meta:  "ed25519, no passphrase.",
	}
	if private := gitKeyTarget(); private != "" {
		row.meta = "ed25519, no passphrase, saved to " + displayKeyPath(private)
	}

	return wiz.markRecommended(row, wiz.gitSuggest == gitChoiceNew)
}

func (wiz Model) markRecommended(row pickerRow, chosen bool) pickerRow {
	if !chosen {
		return row
	}

	// Leave room for the cursor, the gap placeRight keeps, and the badge,
	// so a long path cannot push (Recommended) off the row.
	room := wiz.innerWidth() - choiceBadgePad - lipgloss.Width(recommendedTag)
	if room > 0 {
		row.title = console.TrimWidth(row.title, room)
	}

	row.tag = recommendedTag

	return row
}

func (wiz Model) filterLine() string {
	return "architecture " + shown(wiz.archFilter, "all") + "     cpu " + shown(wiz.cpuFilter, "all")
}

func (wiz Model) emptyList() string {
	if wiz.step == stepMachine && (wiz.archFilter != "" || wiz.cpuFilter != "") {
		return "Nothing matches. Press a or c to widen the filter."
	}

	return "Nothing to choose here."
}

func (wiz Model) liveNote() string {
	value := strings.TrimSpace(wiz.input.Value())
	if value == "" {
		return ""
	}

	switch wiz.step {
	case stepGitKey:
		if config.InvalidPrivateKey(value) == "" {
			return wiz.paint.OK.Render("file found")
		}
	case stepLoad, stepHub, stepAccess, stepToken, stepPreview, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret,
		stepGitIdentity, stepTools, stepImage, stepLocation, stepMachine:
		return ""
	default:
		return ""
	}

	return ""
}

func (wiz Model) listBudget() int {
	chrome := chromeLines
	if wiz.step == stepMachine {
		chrome = machineChrome
	}

	// The hub has no pin and a one-line hint, and its rows must all show.
	if wiz.step == stepHub {
		chrome = hubChrome + wiz.hubExtraLines()
	}

	if wiz.headerBanner() {
		chrome += wordmarkRows - 1
	}

	budget := wiz.height - chrome
	if budget < minListLines {
		return minListLines
	}

	return budget
}

func (wiz Model) pageDelta() int {
	page := wiz.listBudget() / pageSplit
	if page < 1 {
		return 1
	}

	return page
}

func (wiz Model) innerWidth() int {
	width := wiz.width - screenMargin
	if width < fieldWidthMin {
		return fieldWidthMin
	}

	return width
}

func (wiz Model) rule() string {
	return wiz.paint.Rule.Render(strings.Repeat("─", wiz.innerWidth()))
}

func (wiz Model) screen(body string) string {
	return lipgloss.NewStyle().Padding(screenPadY, screenPadX).Render(body)
}

// listWindow is the room a list has: the lines it may use and the width
// its rows wrap at.
type listWindow struct {
	budget int
	width  int
}

func windowRows(rows []pickerRow, selected string, budget, width int) (int, int) {
	if len(rows) == 0 || budget <= 0 {
		return 0, 0
	}

	room := listWindow{budget: budget, width: width}
	index := max(rowIndex(rows, selected), 0)
	start, end, used := index, index+1, room.cost(rows[index])

	for used < budget && (start > 0 || end < len(rows)) {
		var grew bool

		start, used, grew = room.growStart(rows, start, used, grew)

		end, used, grew = room.growEnd(rows, end, used, grew)
		if !grew {
			break
		}
	}

	return start, end
}

func (room listWindow) growStart(rows []pickerRow, start, used int, grew bool) (int, int, bool) {
	if start > 0 && used+room.cost(rows[start-1]) <= room.budget {
		start--

		return start, used + room.cost(rows[start]), true
	}

	return start, used, grew
}

func (room listWindow) growEnd(rows []pickerRow, end, used int, grew bool) (int, int, bool) {
	if end < len(rows) && used+room.cost(rows[end]) <= room.budget {
		return end + 1, used + room.cost(rows[end]), true
	}

	return end, used, grew
}

// cost is the lines a row takes: one, a blank line before it when it is
// spaced, and as many as its meta wraps to.
func (room listWindow) cost(row pickerRow) int {
	lines := 1 + len(metaLines(row, room.width))
	if len(row.facts) > 0 {
		lines += factLineCount(row.facts, room.width-row.pad()) - 1
	}

	if row.spaced() {
		lines++
	}

	return lines
}

func rowIndex(rows []pickerRow, rowID string) int {
	if rowID == "" {
		return firstSelectable(rows)
	}

	for i, row := range rows {
		if !row.skipped() && row.id == rowID {
			return i
		}
	}

	return -1
}

func firstSelectable(rows []pickerRow) int {
	for i, row := range rows {
		if !row.skipped() {
			return i
		}
	}

	return -1
}

func firstID(rows []pickerRow) string {
	index := firstSelectable(rows)
	if index < 0 {
		return ""
	}

	return rows[index].id
}

func lastID(rows []pickerRow) string {
	for _, row := range slices.Backward(rows) {
		if !row.skipped() {
			return row.id
		}
	}

	return ""
}

func findRow(rows []pickerRow, rowID string) (pickerRow, bool) {
	for _, row := range rows {
		if !row.skipped() && row.id == rowID {
			return row, true
		}
	}

	return pickerRow{}, false
}

func nextRow(rows []pickerRow, current string, delta int) string {
	if len(rows) == 0 || delta == 0 {
		return current
	}

	index := rowIndex(rows, current)
	if index < 0 {
		if delta < 0 {
			return lastID(rows)
		}

		return firstID(rows)
	}

	step := 1
	if delta < 0 {
		step = -1
	}

	left := abs(delta)

	for left > 0 {
		next := skipHeaders(rows, index+step, step)
		if next < 0 {
			break
		}

		index = next
		left--
	}

	return rows[index].id
}

func skipHeaders(rows []pickerRow, index, step int) int {
	for index >= 0 && index < len(rows) && rows[index].skipped() {
		index += step
	}

	if index < 0 || index >= len(rows) {
		return -1
	}

	return index
}

func abs(value int) int {
	if value < 0 {
		return -value
	}

	return value
}

func placeRight(width int, left, right string) string {
	if width <= 0 {
		return ""
	}

	if right == "" {
		return console.TrimWidth(left, width)
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap >= 1 {
		return left + strings.Repeat(" ", gap) + right
	}

	keep := width - lipgloss.Width(right) - 1
	if keep < 1 {
		return console.TrimWidth(right, width)
	}

	return console.TrimWidth(left, keep) + " " + right
}

func joinDot(parts ...string) string {
	kept := make([]string, 0, len(parts))

	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, " · ")
}

func shown(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func labelize(value string) string {
	parts := strings.Fields(strings.ReplaceAll(value, "_", " "))
	if len(parts) == 0 {
		return "Other"
	}

	for i, part := range parts {
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}

	return strings.Join(parts, " ")
}

func storageLabel(kind string) string {
	switch kind {
	case "ceph", "network":
		return "network"
	case "":
		return "disk"
	default:
		return kind
	}
}
