package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
)

const (
	welcomeWidth  = 100
	welcomeHeight = 40
	cubeRow       = "████████████████████"
	railAtStart   = "Box ●○○ · Agent ○○○ · Tailnet ○○○"
)

// firstRun is a first run with the catalog in and the logo past its hold:
// the welcome, ready to begin.
func firstRun(t *testing.T, seed config.Config) Model {
	t.Helper()

	wiz := New(seed, testDest, false, adminTailnet(), nil)
	wiz.width, wiz.height = welcomeWidth, welcomeHeight
	wiz.color = true
	wiz.drift = minLoadTicks

	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	model, ok := ready.(Model)
	if !ok || model.step != stepLoad || !model.guided || model.begun {
		t.Fatalf("step %d guided %v begun %v", model.step, model.guided, model.begun)
	}

	return model
}

// agentSeed has the Agent section answered: a real key file, an identity,
// and a tool.
func agentSeed(t *testing.T) config.Config {
	t.Helper()

	key := filepath.Join(t.TempDir(), "git_ed25519")

	err := os.WriteFile(key, []byte("private\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return config.Config{
		GitSSHKeyPath: key,
		GitUserName:   namePlaceholder,
		GitUserEmail:  emailPlaceholder,
		AITools:       config.ToolOpenCode,
	}
}

func enterKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

// expectStep fails unless wiz is on step with rail in its header.
func expectStep(t *testing.T, wiz Model, step stepID, rail string) {
	t.Helper()

	if wiz.step != step || stripANSI(wiz.journey()) != rail {
		t.Fatalf("step %d journey %q, want %d %q", wiz.step, stripANSI(wiz.journey()), step, rail)
	}
}

// reviewed walks a first run through every section to the review, by hand
// on the tailnet, asking for keys.
func reviewed(t *testing.T) Model {
	t.Helper()

	box, _ := firstRun(t, agentSeed(t)).begin()
	machine, _ := box.advance()
	image, _ := machine.advance()
	agent, _ := image.advance()
	identity, _ := agent.advance()
	tools, _ := identity.advance()

	tailnet, _ := tools.advance()
	tailnet.accessChoice = accessManual

	policy, _ := tailnet.advance()

	keys, _ := policy.advance()
	keys.keysChoice = keysChoiceAsk

	hub, _ := keys.confirmKeys()
	expectStep(t, hub, stepHub, "")

	return hub
}

func TestWelcomeShowsThePlanWhileLoading(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{}, testDest, false, adminTailnet(), nil)
	wiz.width, wiz.height = welcomeWidth, welcomeHeight
	wiz.color = true

	// While the catalog loads: the plan, the tailnet, and the wait.
	loading := stripANSI(wiz.View())
	assertViewContains(t, loading, bannerTagline, welcomeIntro, "Box", "Agent", "Tailnet", "Tailscale",
		emailPlaceholder+" · example.com · admin", readingText, quitHint, cubeRow)

	if strings.Contains(loading, welcomeKeys) || strings.Contains(loading, textHeading) {
		t.Fatalf("loading welcome:\n%s", loading)
	}

	// The catalog is in, but the logo keeps its minimum.
	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	loaded, ok := ready.(Model)
	if !ok || loaded.step != stepLoad || loaded.ready() {
		t.Fatalf("step %d ready %v", loaded.step, loaded.ready())
	}

	view := stripANSI(loaded.View())
	assertViewContains(t, view, "1 location · 1 server type")

	if strings.Contains(view, welcomeKeys) || strings.Contains(view, readingText) {
		t.Fatalf("loaded welcome:\n%s", view)
	}
}

func TestEnterBeginsAtTheFirstSection(t *testing.T) {
	t.Parallel()

	loaded := firstRun(t, config.Config{})
	loaded.drift = 0

	// A key cuts the hold short, as on the loading screen: enter begins at
	// once, another key brings the keys line forward.
	if early, _ := loaded.onKey(enterKey()); early.step != stepLocation {
		t.Fatalf("enter went to step %d during the hold", early.step)
	}

	shown, _ := loaded.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if shown.step != stepLoad || !shown.ready() {
		t.Fatalf("step %d ready %v", shown.step, shown.ready())
	}

	assertViewContains(t, stripANSI(shown.View()), welcomeKeys)

	// Enter begins at the first section, with the catalog's choices as the
	// saved state.
	begun, _ := shown.onKey(enterKey())
	expectStep(t, begun, stepLocation, railAtStart)

	if !begun.begun || begun.unsaved() {
		t.Fatalf("begun %v unsaved %v", begun.begun, begun.unsaved())
	}

	assertViewContains(t, stripANSI(begun.View()), railAtStart)

	// Esc on that step returns to the welcome.
	back, _ := begun.back()
	expectStep(t, back, stepLoad, "")

	if !back.guided {
		t.Fatal("esc to the welcome ended the first run")
	}

	assertViewContains(t, stripANSI(back.View()), welcomeKeys)
}

func TestWelcomeTakesOnlyEnterAndU(t *testing.T) {
	t.Parallel()

	wiz := firstRun(t, config.Config{})

	press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")}
	if _, cmd := wiz.onKey(press); cmd != nil {
		t.Fatal("u copied something without a newer release")
	}

	wiz.update = testUpdate()

	assertViewContains(t, stripANSI(wiz.View()), "buildbox v0.2.0 is out")

	if _, cmd := wiz.onKey(press); cmd == nil {
		t.Fatal("u did not copy the update command")
	}

	// Quitting from the welcome never asks: nothing has been chosen yet.
	if quit, _ := wiz.requestQuit(); !quit.quit || quit.askingQuit {
		t.Fatalf("quit %v asking %v", quit.quit, quit.askingQuit)
	}
}

func TestFirstRunWalksTheSectionsInOrder(t *testing.T) {
	t.Parallel()

	box, _ := firstRun(t, agentSeed(t)).begin()
	expectStep(t, box, stepLocation, railAtStart)

	machine, _ := box.advance()
	image, _ := machine.advance()

	// Box's last step leads to Agent's first, and esc there leads back.
	agent, _ := image.advance()
	expectStep(t, agent, stepGitKey, "Box ●●● · Agent ●○○ · Tailnet ○○○")

	back, _ := agent.back()
	expectStep(t, back, stepImage, "Box ●●● · Agent ○○○ · Tailnet ○○○")

	identity, _ := agent.advance()
	tools, _ := identity.advance()

	tailnet, _ := tools.advance()
	expectStep(t, tailnet, stepAccess, "Box ●●● · Agent ●●● · Tailnet ●○○")

	// The tailnet's dots follow its choices.
	tailnet.accessChoice = accessManual

	policy, _ := tailnet.advance()
	expectStep(t, policy, stepPolicy, "Box ●●● · Agent ●●● · Tailnet ●●○○○")
}

func TestTheLastSectionEndsOnTheReview(t *testing.T) {
	t.Parallel()

	hub := reviewed(t)
	if hub.hubChoice != hubWrite || hub.guided || !hub.tailnetDecided {
		t.Fatalf("cursor %q guided %v decided %v", hub.hubChoice, hub.guided, hub.tailnetDecided)
	}

	review := stripANSI(hub.View())
	assertViewContains(t, review, reviewStepTitle, "Enter on a section changes it", "enter write")

	if strings.Contains(review, hubStepTitle) {
		t.Fatalf("review:\n%s", review)
	}

	// From the review, a section returns to it, with the section's own
	// count in the header.
	hub.hubChoice = hubBox

	again, _ := hub.onHub(enterKey())
	expectStep(t, again, stepLocation, "")
	assertViewContains(t, stripANSI(again.View()), "Box · 1 of 3")

	again, _ = again.advance()
	again, _ = again.advance()

	last, _ := again.advance()
	expectStep(t, last, stepHub, "")

	if last.hubChoice != hubWrite {
		t.Fatalf("cursor %q", last.hubChoice)
	}
}

func TestFirstRunSkipsATailnetSetUpBefore(t *testing.T) {
	t.Parallel()

	seed := agentSeed(t)
	seed.OAuthClientID = "k1"
	seed.OAuthClientSecret = testSecret

	wiz := New(seed, testDest, false, adminTailnet(), nil)
	wiz.width, wiz.height = welcomeWidth, welcomeHeight
	wiz.drift = minLoadTicks

	assertViewContains(t, stripANSI(wiz.View()), "checking the OAuth client from an earlier run")

	refused, _ := wiz.onSeedChecked(seedChecked{err: abort.New("The Tailscale API refused the OAuth client")})
	assertViewContains(t, stripANSI(refused.View()), "was refused", "from one access token")

	works, _ := wiz.onSeedChecked(seedChecked{})

	ready, _ := works.onReady(catalogReady{cat: loadedCatalog()})

	welcome, ok := ready.(Model)
	if !ok || welcome.step != stepLoad || !welcome.ready() {
		t.Fatalf("step %d ready %v", welcome.step, welcome.ready())
	}

	assertViewContains(t, stripANSI(welcome.View()), "OAuth client from an earlier run, still valid", "skipped")

	box, _ := welcome.begin()
	expectStep(t, box, stepLocation, "Box ●○○ · Agent ○○○ · Tailnet "+okMark)

	machine, _ := box.advance()
	image, _ := machine.advance()
	agent, _ := image.advance()
	identity, _ := agent.advance()
	tools, _ := identity.advance()

	hub, _ := tools.advance()
	expectStep(t, hub, stepHub, "")

	if hub.hubChoice != hubWrite || hub.guided {
		t.Fatalf("cursor %q guided %v", hub.hubChoice, hub.guided)
	}
}

func TestJourneyFallsBackToTheCountOnANarrowTerminal(t *testing.T) {
	t.Parallel()

	box, _ := firstRun(t, config.Config{}).begin()
	box.width = narrowWidth

	view := stripANSI(box.View())
	if strings.Contains(view, "Box "+railDone) || !strings.Contains(view, "Box ·") {
		t.Fatalf("narrow header:\n%s", view)
	}
}

func TestWelcomeShrinksToTheTerminal(t *testing.T) {
	t.Parallel()

	wiz := firstRun(t, config.Config{})

	tall := stripANSI(wiz.View())
	assertViewContains(t, tall, cubeRow, welcomeIntro)

	wiz.width, wiz.height = fallbackWidth, fallbackHeight

	short := stripANSI(wiz.View())
	assertViewContains(t, short, bannerTagline, welcomeIntro, welcomeKeys)

	if strings.Contains(short, cubeRow) || strings.Contains(short, textHeading) {
		t.Fatalf("welcome at %dx%d:\n%s", wiz.width, wiz.height, short)
	}

	wiz.width, wiz.height = narrowWidth, shortHeight

	plain := stripANSI(wiz.View())
	assertViewContains(t, plain, textHeading, "Tailscale", welcomeKeys)

	if strings.Contains(plain, bannerTagline) {
		t.Fatalf("welcome at %dx%d:\n%s", wiz.width, wiz.height, plain)
	}
}

func TestWelcomeFitsTheTerminalAtEverySize(t *testing.T) {
	t.Parallel()

	loading := New(config.Config{}, testDest, false, adminTailnet(), nil)

	ready := firstRun(t, config.Config{})

	updated := ready
	updated.update = testUpdate()

	seeded := config.Config{OAuthClientID: "k1", OAuthClientSecret: testSecret}

	pending := New(seeded, testDest, false, adminTailnet(), nil)

	refused, _ := pending.onSeedChecked(seedChecked{err: abort.New("refused")})

	works, _ := firstRun(t, seeded).onSeedChecked(seedChecked{})

	for _, wiz := range []Model{loading, ready, updated, pending, refused, works} {
		wiz.color = true

		for width := narrowWidth; width <= sweepMaxWidth; width += sweepWidthStep {
			for height := shortHeight; height <= sweepMaxHeight; height++ {
				wiz.width, wiz.height = width, height

				view := strings.TrimRight(wiz.View(), "\n")
				if lines := strings.Count(view, "\n") + 1; lines > height {
					t.Fatalf("%dx%d draws %d lines:\n%s", width, height, lines, stripANSI(view))
				}
			}
		}
	}
}
