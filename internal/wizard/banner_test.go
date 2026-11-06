package wizard

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/release"
)

const (
	bannerTestWidth = 90
	longCatalog     = 24
	sweepMaxWidth   = 120
	sweepMaxHeight  = 40
	sweepWidthStep  = 6
	textTitle       = "buildbox init"
)

// testUpdate is a newer release the module proxy reported.
func testUpdate() updateFound {
	return updateFound{current: "v0.1.0", latest: "v0.2.0"}
}

func TestCubePiecesDriftAndLoop(t *testing.T) {
	t.Parallel()

	start := cubeLines(0)
	if slices.Equal(start, cubeLines(driftTicks)) {
		t.Fatal("the pieces did not move after one step")
	}

	if !slices.Equal(start, cubeLines(driftTicks*pieceGap)) {
		t.Fatal("the drift does not loop after one gap")
	}
}

func TestLoadingScreenShowsTheLogoWhenItFits(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepLoad, adminTailnet())
	wiz.color = true
	wiz.width, wiz.height = fallbackWidth, fallbackHeight

	view := stripANSI(wiz.View())
	assertViewContains(t, view, bannerTagline, wiz.loadingText())

	if strings.Contains(view, textTitle) || strings.Count(view, "\n") >= wiz.height {
		t.Fatalf("logo view at %dx%d:\n%s", wiz.width, wiz.height, view)
	}

	wiz.height = len(wiz.heroLines())

	view = stripANSI(wiz.View())
	assertViewContains(t, view, textTitle)

	if strings.Contains(view, bannerTagline) {
		t.Fatalf("a short terminal got the logo:\n%s", view)
	}
}

func TestLoadingScreenLeavesOutTheCubeWithoutColor(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepLoad, adminTailnet())
	wiz.color = true
	withCube := len(wiz.heroLines())
	wiz.color = false

	if got, want := withCube-len(wiz.heroLines()), len(cubeLines(0))+1; got != want {
		t.Fatalf("color off removed %d lines, want the cube's %d", got, want)
	}
}

// sectionSteps are the screens inside the sections, each with its own header.
func sectionSteps() []stepID {
	return []stepID{
		stepLocation, stepMachine, stepImage, stepGitKey, stepGitIdentity, stepTools, stepAccess, stepToken,
		stepPreview, stepPolicy, stepKeys, stepOAuthID, stepOAuthSecret,
	}
}

// screenAt is step drawn over a catalog long enough for its lists to scroll.
func screenAt(step stepID) Model {
	wiz := New(config.Config{}, testDest, false, adminTailnet(), nil)
	wiz.cat = loadedCatalog()

	for i := range longCatalog {
		wiz.cat.Machines = append(wiz.cat.Machines, machine(fmt.Sprintf("cx%d", i), true, false, "0.01", "6.00"))
	}

	wiz.loaded = true
	wiz.location = config.SeedLocation
	wiz.step = step

	// Opened directly, as on a later run: a first run's screens are
	// reached through the welcome, and firstrun_test covers them.
	wiz.guided = false

	if step == stepPolicy || step == stepKeys || step == stepOAuthID || step == stepOAuthSecret {
		wiz.accessChoice = accessManual
	}

	next, _ := wiz.prepareStep()

	return next
}

func TestEveryScreenShowsTheWordmarkOnceTheTerminalIsTallEnough(t *testing.T) {
	t.Parallel()

	for _, step := range append(sectionSteps(), stepHub) {
		wiz := screenAt(step)
		wiz.width, wiz.height = bannerTestWidth, bannerMinHeight

		view := stripANSI(wiz.View())
		if !strings.Contains(view, stripANSI(wordmarkLines()[1])) || strings.Contains(view, textTitle) {
			t.Fatalf("step %d at %d rows:\n%s", step, wiz.height, view)
		}

		wiz.height--

		assertViewContains(t, stripANSI(wiz.View()), textTitle)
	}
}

func TestEveryScreenFitsWithTheWordmark(t *testing.T) {
	t.Parallel()

	for _, step := range sectionSteps() {
		wiz := screenAt(step)

		for width := narrowWidth; width <= sweepMaxWidth; width += sweepWidthStep {
			for height := bannerMinHeight; height <= sweepMaxHeight; height++ {
				wiz.width, wiz.height = width, height

				view := strings.TrimRight(wiz.View(), "\n")
				if lines := strings.Count(view, "\n") + 1; lines > height {
					t.Fatalf("step %d at %dx%d draws %d lines:\n%s", step, width, height, lines, stripANSI(view))
				}
			}
		}
	}
}

func TestHubFitsTheTerminalAtEverySize(t *testing.T) {
	t.Parallel()

	plain := tailnetScreen(stepHub, adminTailnet())
	plain.tailnetDecided = true

	noted := plain
	noted.note = "The client from the config was refused. A new OAuth client is made here."

	updated := plain
	updated.update = testUpdate()

	both := noted
	both.update = updated.update

	for _, wiz := range []Model{plain, noted, updated, both} {
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

func TestLoadingScreenStaysUpForItsMinimum(t *testing.T) {
	t.Parallel()

	ready, _ := New(config.Config{}, testDest, true, adminTailnet(), nil).onReady(catalogReady{cat: loadedCatalog()})

	wiz, isModel := ready.(Model)
	if !isModel || wiz.step != stepLoad {
		t.Fatal("a fast catalog skipped the loading screen")
	}

	for tick := 1; tick <= minLoadTicks; tick++ {
		next, _ := wiz.Update(wiz.spin.Tick())

		wiz, isModel = next.(Model)
		if !isModel || (wiz.step == stepLoad) != (tick < minLoadTicks) {
			t.Fatalf("after %d ticks the step is %d", tick, wiz.step)
		}
	}
}

func TestKeySkipsTheLoadingScreenOnceTheCatalogIsIn(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{}, testDest, true, adminTailnet(), nil)
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	if waiting, _ := wiz.onKey(enter); waiting.step != stepLoad {
		t.Fatal("a key left the loading screen before the catalog came in")
	}

	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	loaded, ok := ready.(Model)
	if !ok {
		t.Fatal("onReady returned another model")
	}

	skipped, _ := loaded.onKey(enter)
	if skipped.step != stepHub || !skipped.begun {
		t.Fatalf("a key left step %d, begun %v", skipped.step, skipped.begun)
	}
}

func TestHubShowsANewerReleaseAndHowToGetIt(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepHub, adminTailnet())
	wiz.tailnetDecided = true
	wiz.width, wiz.height = bannerTestWidth, sweepMaxHeight

	if strings.Contains(stripANSI(wiz.View()), " is out ") {
		t.Fatal("an update notice without a newer release")
	}

	next, _ := wiz.Update(testUpdate())

	updated, isModel := next.(Model)
	if !isModel {
		t.Fatal("Update returned another model")
	}

	assertViewContains(t, stripANSI(updated.View()), "buildbox v0.2.0 is out (you have v0.1.0)", release.UpdateCommand,
		updateKey)

	// A note does not push the notice out.
	updated.note = "a note"
	assertViewContains(t, stripANSI(updated.View()), "a note", "buildbox v0.2.0 is out")
}

func TestUCopiesTheUpdateCommandOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepHub, adminTailnet())
	press := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")}

	if _, cmd := wiz.onHub(press); cmd != nil {
		t.Fatal("u copied something without a newer release")
	}

	if strings.Contains(wiz.hubFooter(), updateKey) {
		t.Fatalf("footer offers u without a newer release: %q", wiz.hubFooter())
	}

	wiz.update = testUpdate()

	if _, cmd := wiz.onHub(press); cmd == nil {
		t.Fatal("u did not copy the update command")
	}

	done := wiz.onCopied(copied{ok: true, done: "The update command is on the clipboard.", failed: "no"})
	if done.note != "The update command is on the clipboard." {
		t.Fatalf("note %q", done.note)
	}
}
