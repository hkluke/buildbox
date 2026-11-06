package wizard

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/tailscale"
)

const (
	savedClientID = "k-saved"
	ownerOnlyPerm = 0o600
	escKey        = "esc"
	ctrlCKey      = "ctrl+c"
)

// overview is a run with no config yet that has reached the overview the
// way a later run does: straight from the loading screen, past the welcome.
func overview(t *testing.T) Model {
	t.Helper()

	wiz := New(config.Config{}, filepath.Join(t.TempDir(), "config.toml"), false, adminTailnet(), nil)
	wiz.drift = minLoadTicks
	wiz.guided = false

	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	hub, isModel := ready.(Model)
	if !isModel || hub.step != stepHub {
		t.Fatal("the form did not reach the overview")
	}

	return hub
}

func press(key string) tea.KeyMsg {
	switch key {
	case escKey:
		return tea.KeyMsg{Type: tea.KeyEsc}
	case ctrlCKey:
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// findSaved runs cmd, and the commands of a batch, until one saves the
// OAuth client.
func findSaved(t *testing.T, cmd tea.Cmd) clientSaved {
	t.Helper()

	if cmd == nil {
		t.Fatal("no command to save the OAuth client")
	}

	switch msg := cmd().(type) {
	case clientSaved:
		return msg
	case tea.BatchMsg:
		for _, inner := range msg {
			if inner == nil {
				continue
			}

			if saved, found := inner().(clientSaved); found {
				return saved
			}
		}
	}

	t.Fatal("no command saved the OAuth client")

	return clientSaved{}
}

func TestTheOAuthClientIsSavedTheMomentItIsCreated(t *testing.T) {
	t.Parallel()

	wiz := overview(t)
	wiz.step = stepPreview

	next, cmd := wiz.settleApply(setupApplied{client: tailscale.OAuthClient{ID: savedClientID, Secret: testSecret}})

	saved := findSaved(t, cmd)
	if saved.err != nil || saved.path != config.CredentialsPath(wiz.dest) {
		t.Fatalf("saved %+v", saved)
	}

	info, err := os.Stat(saved.path)
	if err != nil || info.Mode().Perm() != ownerOnlyPerm {
		t.Fatalf("credentials file %v, %v", info, err)
	}

	reread, err := config.SeedCredentials(wiz.dest, config.Config{})
	if err != nil || reread.OAuthClientID != savedClientID || reread.OAuthClientSecret != testSecret {
		t.Fatalf("reread %+v, %v", reread, err)
	}

	if after := next.onClientSaved(saved); after.unsaved() {
		t.Fatal("a saved client still counts as unsaved")
	}
}

func TestQuittingWithNothingChangedNeedsNoQuestion(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"q", escKey, ctrlCKey} {
		next, cmd := overview(t).onKey(press(key))
		if !next.quit || cmd == nil {
			t.Fatalf("%s did not quit an unchanged form", key)
		}
	}

	loading := New(config.Config{Tag: config.DefaultTag}, testDest, false, adminTailnet(), nil)
	if next, _ := loading.onKey(press("q")); !next.quit {
		t.Fatal("q did not quit the loading screen")
	}
}

func TestQuittingWithChangesAsksFirst(t *testing.T) {
	t.Parallel()

	changed := overview(t)
	changed.location = "hel1"

	for _, key := range []string{"q", escKey, ctrlCKey} {
		asked, cmd := changed.onKey(press(key))
		if asked.quit || cmd != nil || !asked.askingQuit {
			t.Fatalf("%s quit without asking", key)
		}

		assertViewContains(t, stripANSI(asked.View()), "Quit without writing?")

		stayed, _ := asked.onKey(press("n"))
		if stayed.quit || stayed.askingQuit {
			t.Fatalf("n after %s did not go back", key)
		}

		for _, answer := range []string{"y", ctrlCKey} {
			if left, _ := asked.onKey(press(answer)); !left.quit {
				t.Fatalf("%s after %s did not quit", answer, key)
			}
		}
	}
}

func TestANewGitKeyCountsAsUnsaved(t *testing.T) {
	t.Parallel()

	wiz := overview(t)
	wiz.gitPublicKey = "ssh-ed25519 AAAA buildbox git key"

	if asked, _ := wiz.onKey(press("q")); !asked.askingQuit {
		t.Fatal("quitting would lose the new key's public half without asking")
	}
}

func TestAPastedOAuthClientIsSavedOnceItChecksOut(t *testing.T) {
	t.Parallel()

	wiz := overview(t)
	wiz.step = stepOAuthSecret
	wiz.accessChoice = accessManual
	wiz.oauthID = savedClientID
	wiz.oauthSecret = testSecret

	_, cmd := wiz.settleCheck(clientChecked{})

	if saved := findSaved(t, cmd); saved.err != nil || saved.id != savedClientID {
		t.Fatalf("saved %+v", saved)
	}
}

func TestCtrlOOpensThePageATypingStepPointsTo(t *testing.T) {
	t.Parallel()

	for step, page := range map[stepID]string{stepToken: tailscale.KeysURL, stepOAuthID: tailscale.OAuthURL} {
		wiz := screenAt(step)

		if wiz.stepPage() != page {
			t.Fatalf("step %d points to %q", step, wiz.stepPage())
		}

		next, cmd := wiz.onKey(tea.KeyMsg{Type: tea.KeyCtrlO})
		if cmd == nil || next.input.Value() != wiz.input.Value() {
			t.Fatalf("step %d: ctrl+o did not open the page, or typed into the field", step)
		}

		assertViewContains(t, stripANSI(wiz.View()), "ctrl+o open the page")
	}

	if screenAt(stepOAuthSecret).stepPage() != "" {
		t.Fatal("the secret step points to a page")
	}
}

func TestHintsKeepTheirLinesAndIndentWrappedSteps(t *testing.T) {
	t.Parallel()

	const narrow = 24

	lines := wrapHintLine("2. Click Generate access token, with an expiry of 1 day.", narrow)
	if len(lines) < 2 || lines[0][:len(stepMarker)] != "2. " {
		t.Fatalf("lines = %q", lines)
	}

	for _, line := range lines[1:] {
		if line[:len(stepMarker)] != "   " || len(line) > narrow {
			t.Fatalf("wrapped line %q is not under its step's text", line)
		}
	}

	token := screenAt(stepToken)
	assertViewContains(t, stripANSI(token.View()), "  1. Open "+tailscale.KeysURL, "  3. Paste the token here.",
		"  The token is used once, for this setup, and never stored.")
}
