package wizard

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
	"github.com/hkluke/buildbox/internal/release"
)

// section is one of the three parts of the hub: a short run of steps that
// ends back on the hub.
type section int

const (
	sectionBox section = iota
	sectionAgent
	sectionTailnet
)

const (
	hubStepTitle    = "Overview"
	reviewStepTitle = "Review"
	providerName    = "Hetzner"
	hubLabelWidth   = 10
	hubMetaPad      = 2 + hubLabelWidth
	factGap         = 3
	factLabelPad    = 11
	okMark          = "✓"
	notChosen       = "not chosen yet"
	openKeys        = "↑↓ move  ·  enter open"
	writeKeys       = "↑↓ move  ·  enter write"
	updateKey       = "u copy update"
	quitKey         = "q quit"
	keyGap          = "  ·  "
)

// sectionOf is the section a step belongs to. The load screen and the hub
// belong to none.
func sectionOf(step stepID) (section, bool) {
	switch step {
	case stepLocation, stepMachine, stepImage:
		return sectionBox, true
	case stepGitKey, stepGitIdentity, stepTools:
		return sectionAgent, true
	case stepAccess, stepToken, stepPreview, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret:
		return sectionTailnet, true
	case stepLoad, stepHub:
		return sectionBox, false
	default:
		return sectionBox, false
	}
}

func sectionName(sec section) string {
	switch sec {
	case sectionBox:
		return "Box"
	case sectionAgent:
		return "Agent"
	case sectionTailnet:
		return "Tailnet"
	default:
		return ""
	}
}

// sectionRow is the hub row that opens a section.
func sectionRow(sec section) string {
	switch sec {
	case sectionBox:
		return hubBox
	case sectionAgent:
		return hubAgent
	case sectionTailnet:
		return hubTailnet
	default:
		return ""
	}
}

func sectionFor(rowID string) (section, bool) {
	switch rowID {
	case hubBox:
		return sectionBox, true
	case hubAgent:
		return sectionAgent, true
	case hubTailnet:
		return sectionTailnet, true
	default:
		return sectionBox, false
	}
}

// sectionSteps is the path through one section. The tailnet's depends on
// the choices made inside it, so it is read again after each choice.
func (wiz Model) sectionSteps(sec section) []stepID {
	switch sec {
	case sectionBox:
		return []stepID{stepLocation, stepMachine, stepImage}
	case sectionAgent:
		return []stepID{stepGitKey, stepGitIdentity, stepTools}
	case sectionTailnet:
		return wiz.tailnetSteps()
	default:
		return nil
	}
}

// tailnetSteps: the token path sets the tailnet up from the preview; the
// manual path shows the policy lines and asks how keys are made, with the
// OAuth client steps only when buildbox mints them.
func (wiz Model) tailnetSteps() []stepID {
	switch {
	case wiz.accessChoice == accessToken:
		return []stepID{stepAccess, stepToken, stepPreview}
	case wiz.keysChoice == keysChoiceMint:
		return []stepID{stepAccess, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret}
	default:
		return []stepID{stepAccess, stepPolicy, stepKeys}
	}
}

// onHub opens the chosen section, or writes. Enter on a dim Write says what
// is still missing instead.
func (wiz Model) onHub(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "u" && wiz.update.latest != "" {
		return wiz, copyUpdate()
	}

	if msg.String() != keyEnter {
		return wiz.onPicker(msg)
	}

	row, found := findRow(wiz.hubRows(), wiz.hubChoice)
	if !found {
		return wiz, nil
	}

	if row.dim {
		wiz.note = wiz.blockedNote()

		return wiz, nil
	}

	if row.id == hubWrite {
		wiz.confirmed = true

		return wiz, tea.Quit
	}

	sec, ok := sectionFor(row.id)
	if !ok {
		return wiz, nil
	}

	return wiz.openSection(sec)
}

func (wiz Model) openSection(sec section) (Model, tea.Cmd) {
	wiz.step = wiz.sectionSteps(sec)[0]
	wiz.note = ""

	return wiz.prepareStep()
}

// openHub shows the overview with the cursor on choice.
func (wiz Model) openHub(choice string) (Model, tea.Cmd) {
	wiz.step = stepHub
	wiz.hubChoice = choice
	wiz.note = ""
	wiz.gitEditing = false

	return wiz.prepareStep()
}

// finishSection returns to the hub after a section's last step, or on a
// first run opens the next section, and the hub after the last one. The
// tailnet counts as decided from then on: the user has seen the choices and
// made one.
func (wiz Model) finishSection(sec section) (Model, tea.Cmd) {
	if sec == sectionTailnet {
		wiz.tailnetDecided = true
	}

	if wiz.guided {
		if next, ok := wiz.nextGuidedSection(sec); ok {
			return wiz.openSection(next)
		}

		wiz.guided = false
	}

	return wiz.openHub(wiz.hubCursor())
}

// hubQuestion is the hub's title and hint: a review on a first run, since
// the sections were just walked through, and an overview after.
func (wiz Model) hubQuestion() [2]string {
	if !wiz.replacing {
		return [2]string{reviewStepTitle, "Enter on a section changes it. Write saves the config."}
	}

	return [2]string{hubStepTitle, "Enter opens a section. Write saves the config."}
}

// copyUpdate puts the update command on the clipboard.
func copyUpdate() tea.Cmd {
	return copyText(release.UpdateCommand, "The update command is on the clipboard.",
		"No clipboard tool found. Copy the command by hand.")
}

// hubCursor is the row the cursor lands on when the hub opens: the first
// section that still needs something, or Write when none does.
func (wiz Model) hubCursor() string {
	switch {
	case !wiz.boxComplete():
		return hubBox
	case wiz.agentProblem() != "":
		return hubAgent
	case !wiz.tailnetDecided:
		return hubTailnet
	default:
		return hubWrite
	}
}

func keepHubChoice(current, rowID string) string {
	if _, ok := sectionFor(rowID); ok || rowID == hubWrite {
		return rowID
	}

	return current
}

// boxComplete reports whether the chosen server type can be created where
// the box runs, with an image to boot.
func (wiz Model) boxComplete() bool {
	_, place, ok := wiz.cat.Place(wiz.serverType, wiz.location)

	return ok && place.Creatable && wiz.image != ""
}

// agentProblem is what the Agent section still needs, or empty.
func (wiz Model) agentProblem() string {
	switch {
	case strings.TrimSpace(wiz.gitKey) == "":
		return "No Git key yet. Open to generate one."
	case config.InvalidPrivateKey(wiz.gitKey) != "":
		return "No usable Git key at " + console.DisplayPath(wiz.gitKey) + ". Open to generate or choose one."
	case config.InvalidPersonName(wiz.gitName) != "" || config.InvalidEmail(wiz.gitEmail) != "":
		return "Git name or email is missing."
	}

	_, err := config.ParseTools(wiz.tools)
	if err != nil {
		return "A tool in the config is unknown. Open to choose again."
	}

	return ""
}

// writeBlockers names the sections Write still waits for.
func (wiz Model) writeBlockers() []string {
	blockers := []string{}

	if !wiz.boxComplete() {
		blockers = append(blockers, sectionName(sectionBox))
	}

	if wiz.agentProblem() != "" {
		blockers = append(blockers, sectionName(sectionAgent))
	}

	if !wiz.tailnetDecided {
		blockers = append(blockers, sectionName(sectionTailnet))
	}

	return blockers
}

// hubRows are the sections, a blank line apart: what is set, in a few
// words. Details live in the sections. A line in the warning color appears
// under a row for a problem.
func (wiz Model) hubRows() []pickerRow {
	rows := []pickerRow{wiz.boxRow(), wiz.agentRow(), wiz.tailnetRow(), wiz.writeRow()}
	for i := range rows[1:] {
		rows[i+1].gap = true
	}

	return rows
}

// hubFact is one fact on a hub row, as a label and a value. The label is
// drawn muted and the value in the row's own color, so the eye lands on the
// values. A fact without a label is just a value. An ok fact ends with a
// green check: something is ready.
type hubFact struct {
	label string
	value string
	ok    bool
}

func (fact hubFact) plain() string {
	text := fact.value
	if fact.label != "" {
		text = fact.label + " " + text
	}

	if fact.ok {
		text += " " + okMark
	}

	return text
}

// check is the green mark after an ok fact, or nothing.
func (wiz Model) check(fact hubFact) string {
	if !fact.ok {
		return ""
	}

	return " " + wiz.paint.OK.Render(okMark)
}

func value(text string) hubFact {
	return hubFact{value: text}
}

// hubRow lines the section names up in a column, with the facts after.
// Facts that do not fit the line continue under it, aligned with the first,
// so nothing is cut on a narrow terminal.
func (wiz Model) hubRow(id, name string, facts ...hubFact) pickerRow {
	return pickerRow{id: id, title: name, facts: facts, metaPad: hubMetaPad}
}

// factLines packs the facts into lines of room width, each fact whole.
func factLines(facts []hubFact, room int) [][]hubFact {
	lines := make([][]hubFact, 0, 1)
	line := make([]hubFact, 0, len(facts))
	used := 0

	for _, fact := range facts {
		width := lipgloss.Width(fact.plain())
		if len(line) > 0 && used+factGap+width > room {
			lines = append(lines, line)
			line = make([]hubFact, 0, len(facts))
			used = 0
		}

		if len(line) > 0 {
			used += factGap
		}

		line = append(line, fact)
		used += width
	}

	if len(line) > 0 {
		lines = append(lines, line)
	}

	return lines
}

// viewFactRow draws a hub row: the section name in a column, then the facts
// in two shades, then any problem line.
func (wiz Model) viewFactRow(row pickerRow) string {
	room := wiz.innerWidth() - row.pad()
	name, label, value := wiz.factStyles(row)

	mark := "  "
	if row.id == wiz.selectedID() {
		mark = "▸ "
	}

	texts := wiz.factTexts(row.facts, label, value, room)
	lines := make([]string, 0, len(texts))

	for i, text := range texts {
		lead := strings.Repeat(" ", row.pad())
		if i == 0 {
			lead = mark + name.Render(console.PadRight(row.title, hubLabelWidth))
		}

		lines = append(lines, lead+text)
	}

	return strings.Join(append(lines, wiz.viewMeta(row)...), "\n")
}

// labeled reports whether the facts carry labels. Labeled facts are listed
// one per line, values in a column; bare values share a line.
func labeled(facts []hubFact) bool {
	for _, fact := range facts {
		if fact.label != "" {
			return true
		}
	}

	return false
}

// factTexts are the rendered lines after the section name.
func (wiz Model) factTexts(facts []hubFact, label, value lipgloss.Style, room int) []string {
	if !labeled(facts) {
		packed := factLines(facts, room)
		lines := make([]string, 0, len(packed))

		for _, line := range packed {
			lines = append(lines, wiz.renderFacts(line, label, value, room))
		}

		return lines
	}

	lines := make([]string, 0, len(facts))

	for _, fact := range facts {
		parts := valueLines(fact, room)

		for index, part := range parts {
			head := strings.Repeat(" ", factLabelPad)
			if index == 0 {
				head = label.Render(console.PadRight(fact.label, factLabelPad))
			}

			tail := ""
			if index == len(parts)-1 {
				tail = wiz.check(fact)
			}

			lines = append(lines, head+value.Render(part)+tail)
		}
	}

	return lines
}

// valueLines wraps a labeled fact's value to the column it sits in, so a
// long price or path continues under itself rather than being cut.
func valueLines(fact hubFact, room int) []string {
	width := max(room-factLabelPad, 1)

	return strings.Split(console.WrapText(pieces(fact.value, width), width), "\n")
}

// pieces splits a word longer than width, such as a path, where the wrap
// would otherwise cut it with an ellipsis.
func pieces(text string, width int) string {
	words := strings.Fields(text)

	for index, word := range words {
		runes := []rune(word)
		if len(runes) <= width {
			continue
		}

		parts := make([]string, 0, len(runes)/width+1)
		for len(runes) > width {
			parts = append(parts, string(runes[:width]))
			runes = runes[width:]
		}

		words[index] = strings.Join(append(parts, string(runes)), " ")
	}

	return strings.Join(words, " ")
}

// factLineCount is how many lines the facts take, for the list window.
func factLineCount(facts []hubFact, room int) int {
	if !labeled(facts) {
		return len(factLines(facts, room))
	}

	count := 0
	for _, fact := range facts {
		count += len(valueLines(fact, room))
	}

	return count
}

// renderFacts styles one line of facts. A lone fact wider than the room is
// cut as plain text, before any color is added.
func (wiz Model) renderFacts(facts []hubFact, label, value lipgloss.Style, room int) string {
	parts := make([]string, 0, len(facts))

	for _, fact := range facts {
		text := value.Render(console.TrimWidth(fact.value, room)) + wiz.check(fact)
		if fact.label != "" {
			text = label.Render(fact.label) + " " + text
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, wiz.paint.Muted.Render(" · "))
}

// factStyles are the shades of a hub row's name, fact labels, and fact
// values: the values carry the row's color, the labels stay muted.
func (wiz Model) factStyles(row pickerRow) (lipgloss.Style, lipgloss.Style, lipgloss.Style) {
	selected := row.id == wiz.selectedID()

	switch {
	case selected && row.dim:
		return wiz.paint.Warn, wiz.paint.Faint, wiz.paint.Warn
	case selected:
		return wiz.paint.Accent, wiz.paint.Muted, wiz.paint.Accent
	case row.dim:
		return wiz.paint.Faint, wiz.paint.Faint, wiz.paint.Faint
	default:
		return wiz.paint.Text, wiz.paint.Muted, wiz.paint.Text
	}
}

func (wiz Model) boxRow() pickerRow {
	_, place, ok := wiz.cat.Place(wiz.serverType, wiz.location)
	if !ok || !place.Creatable || wiz.image == "" {
		row := wiz.hubRow(hubBox, sectionName(sectionBox), value(notChosen))
		row.meta = "Open to pick a location, a server type, and an image."
		row.warn = true

		return row
	}

	// The firewall is not here: Write creates a missing one and says so, and
	// create checks its rules on every run.
	return wiz.hubRow(hubBox, sectionName(sectionBox),
		hubFact{label: "provider", value: providerName},
		hubFact{label: "type", value: wiz.serverType},
		hubFact{label: "location", value: wiz.placeName()},
		hubFact{label: "image", value: wiz.image},
		hubFact{label: "price", value: priceSentence(wiz.cat.Currency, place)},
	)
}

// agentRow shows the Git side first, what reaches the git host and as whom
// it commits, then the tools the agent gets. The user it runs as is fixed,
// so it is not a setting to show.
func (wiz Model) agentRow() pickerRow {
	tools := config.ToolSummary(wiz.tools)
	if len(config.ToolSet(wiz.tools)) == 0 {
		tools = "none"
	}

	row := wiz.hubRow(hubAgent, sectionName(sectionAgent),
		hubFact{label: "git key", value: console.DisplayPath(wiz.gitKey)},
		hubFact{label: "git name", value: wiz.gitName},
		hubFact{label: "git email", value: wiz.gitEmail},
		hubFact{label: "tools", value: tools},
	)

	if problem := wiz.agentProblem(); problem != "" {
		row.meta = problem
		row.warn = true
	}

	return row
}

func (wiz Model) tailnetRow() pickerRow {
	state, problem := wiz.tailnetState()
	ready := hubFact{value: state, ok: problem == "" && (wiz.setUp || wiz.MintsKeys())}
	row := wiz.hubRow(hubTailnet, sectionName(sectionTailnet), value(wiz.tailnetName()), ready)
	row.meta = problem
	row.warn = problem != ""

	return row
}

// tailnetState is the Tailnet row's summary and, when the section still
// needs something, what that is.
func (wiz Model) tailnetState() (string, string) {
	switch {
	case wiz.setUp:
		return "policy updated, OAuth client ready", ""
	case wiz.MintsKeys():
		return "OAuth client ready", ""
	case wiz.tailnetDecided:
		return "a key is asked at each create", ""
	default:
		return "not set up yet", "Open to add the policy lines and the OAuth client."
	}
}

func (wiz Model) writeRow() pickerRow {
	if blockers := wiz.writeBlockers(); len(blockers) > 0 {
		row := wiz.hubRow(hubWrite, "Write", value("after "+console.JoinAnd(blockers)))
		row.dim = true

		return row
	}

	files := filepath.Base(wiz.dest)
	if wiz.MintsKeys() {
		files += " and " + filepath.Base(config.CredentialsPath(wiz.dest))
	}

	if wiz.replacing {
		files = "replaces " + files
	}

	return wiz.hubRow(hubWrite, "Write", value(files))
}

// blockedNote is what enter on a dim Write says.
func (wiz Model) blockedNote() string {
	return "Finish " + console.JoinAnd(wiz.writeBlockers()) + " first."
}

// hubPlace is the directory the files go to, shown where a section shows
// its progress. A checkout's buildbox.toml needs no directory.
func (wiz Model) hubPlace() string {
	dir := filepath.Dir(wiz.dest)
	if dir == "." {
		return ""
	}

	return console.DisplayPath(dir)
}

func (wiz Model) hubFooter() string {
	keys := openKeys
	if wiz.hubChoice == hubWrite && len(wiz.writeBlockers()) == 0 {
		keys = writeKeys
	}

	if wiz.update.latest != "" {
		keys += keyGap + updateKey
	}

	return keys + keyGap + quitKey
}
