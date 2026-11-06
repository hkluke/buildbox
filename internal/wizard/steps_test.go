package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/abort"
	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/hetzner"
	"github.com/hkluke/buildbox/internal/tailscale"
)

// testSecret stands in for an OAuth client secret.
//
//nolint:gosec // G101: a made-up value for the tests.
const testSecret = "tskey-client-secret-value"

// testAccessToken stands in for an API access token.
//
//nolint:gosec // G101: a made-up value for the tests.
const testAccessToken = "tskey-api-test"

func adminTailnet() tailscale.Tailnet {
	return tailscale.Tailnet{Name: "example.com", Login: emailPlaceholder, Admin: true, MagicDNS: true}
}

func TestPolicyStepShowsTheLinesForTheTag(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepPolicy, adminTailnet())
	prose := fold(stripANSI(wiz.View()))
	assertViewContains(
		t,
		prose,
		policyStepTitle,
		`"tag:buildbox": ["autogroup:owner"],`,
		`"dst": ["tag:buildbox:*"]`,
		`"users": ["agent", "admin"]`,
		"autogroup:member",
		"o open the editor",
		"Tailnet · 2 of 5",
	)

	if strings.Contains(prose, "not an admin") || strings.Contains(prose, "root") {
		t.Fatalf("policy view:\n%s", prose)
	}

	outsider := tailnetScreen(stepPolicy, tailscale.Tailnet{Name: "example.com"})
	assertViewContains(t, fold(stripANSI(outsider.View())), "not an admin of example.com")

	text := wiz.policyText()
	wants := []string{
		"// tagOwners\n", "// acls\n", "// ssh\n",
		tailscale.SSHRule(config.DefaultTag),
	}

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestKeysStepRecommendsMinting(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepKeys, adminTailnet())
	view := stripANSI(wiz.View())
	assertViewContains(
		t,
		view,
		keysStepTitle,
		"Let buildbox mint auth keys  "+recommendedTag,
		"auth_keys scope for tag:buildbox",
		"Paste a one-time key for each box",
	)

	if wiz.keysChoice != keysChoiceMint || strings.Count(view, recommendedTag) != 1 {
		t.Fatalf("choice %q:\n%s", wiz.keysChoice, view)
	}

	seeded := New(config.Config{OAuthClientID: "k1", OAuthClientSecret: testSecret}, testDest, false, adminTailnet(), nil)
	seeded.step = stepKeys
	seeded.width = 88
	seeded.height = 32

	assertViewContains(t, stripANSI(seeded.View()), configMeta)
}

func TestAskingForKeysSkipsTheOAuthSteps(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepKeys, adminTailnet())
	wiz.keysChoice = keysChoiceAsk
	wiz.oauthID = "k1"
	wiz.oauthSecret = testSecret

	// Asking ends the section: back on the hub, with the tailnet decided and
	// the cursor on the first section that still needs something.
	next, _ := wiz.confirmKeys()
	if next.step != stepHub || next.oauthID != "" || next.oauthSecret != "" || next.MintsKeys() || !next.tailnetDecided {
		t.Fatalf("step %d id %q secret %q decided %v", next.step, next.oauthID, next.oauthSecret, next.tailnetDecided)
	}

	if next.hubChoice != hubBox || next.progress() != "" {
		t.Fatalf("hub cursor %q progress %q", next.hubChoice, next.progress())
	}
}

func TestMintingOpensTheOAuthSteps(t *testing.T) {
	t.Parallel()

	minting := tailnetScreen(stepKeys, adminTailnet())

	next, _ := minting.confirmKeys()
	if next.step != stepOAuthID || !next.typing() || next.progress() != "Tailnet · 4 of 5" {
		t.Fatalf("step %d typing %v progress %q", next.step, next.typing(), next.progress())
	}

	assertViewContains(t, fold(stripANSI(next.View())), tailscale.OAuthURL, "write for auth_keys")
}

func TestOAuthSecretIsMaskedWhileChecked(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepOAuthSecret, adminTailnet())
	wiz.oauthID = "k1"

	if wiz.input.EchoMode != textinput.EchoPassword {
		t.Fatal("the secret should be masked")
	}

	wiz.input.SetValue(testSecret)

	checking, cmd := wiz.onText(tea.KeyMsg{Type: tea.KeyEnter})
	if !checking.checking || cmd == nil || checking.oauthSecret != testSecret {
		t.Fatalf("checking %v cmd %v secret %q", checking.checking, cmd, checking.oauthSecret)
	}

	view := stripANSI(checking.View())
	if strings.Contains(view, testSecret) || !strings.Contains(view, checkingNote) {
		t.Fatalf("checking view:\n%s", view)
	}

	if wiz.commitPlainProblem("") == "" {
		t.Fatal("an empty secret should be refused")
	}
}

func TestClientCheckDecidesTheNextStep(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepOAuthSecret, adminTailnet())
	wiz.oauthID = "k1"
	wiz.oauthSecret = testSecret
	wiz.checking = true

	refusal := abort.Hint("The Tailscale API refused the OAuth client", "", "")

	refused, _ := wiz.settleCheck(clientChecked{err: refusal})
	if refused.checking || refused.step != stepOAuthSecret || !strings.Contains(refused.note, "refused") {
		t.Fatalf("refused: checking %v step %d note %q", refused.checking, refused.step, refused.note)
	}

	passed, _ := wiz.settleCheck(clientChecked{})
	if passed.checking || passed.step != stepHub || !passed.tailnetDecided || !passed.MintsKeys() {
		t.Fatalf("passed: checking %v step %d decided %v", passed.checking, passed.step, passed.tailnetDecided)
	}
}

func TestHubShowsTheTailnetAndTheKeySource(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepHub, adminTailnet())
	wiz.oauthID = "k1"
	wiz.oauthSecret = testSecret
	wiz.tailnetDecided = true

	// A first run's hub is the review; a later run's the overview.
	view := stripANSI(wiz.View())
	assertViewContains(t, view, reviewStepTitle, "example.com · OAuth client ready "+okMark, notChosen, "No Git key yet")

	wiz.replacing = true
	assertViewContains(t, stripANSI(wiz.View()), hubStepTitle, "Enter opens a section.")
	wiz.replacing = false

	if strings.Contains(view, "Login key") || strings.Contains(view, testSecret) || strings.Contains(view, "…") {
		t.Fatalf("hub view:\n%s", view)
	}

	wiz.oauthID = ""
	wiz.oauthSecret = ""

	assertViewContains(t, stripANSI(wiz.View()), "example.com · a key is asked at each create")

	wiz.tailnetDecided = false

	assertViewContains(t, stripANSI(wiz.View()), "example.com · not set up yet",
		"Open to add the policy lines and the OAuth client.")
}

func TestChoiceScreensMarkOneRecommendation(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{}, testDest, false, adminTailnet(), nil)
	wiz.width = 88
	wiz.height = 36
	wiz.step = stepImage
	wiz.image = config.DefaultImage
	wiz.suggestImage = config.DefaultImage
	wiz.cat.Images = []hetzner.ImageOffer{
		{Name: config.DefaultImage, Detail: "Ubuntu 26.04"},
		{Name: "ubuntu-24.04", Detail: "Ubuntu 24.04"},
	}

	imageView := stripANSI(wiz.View())
	assertViewContains(t, imageView, config.DefaultImage+"  "+recommendedTag)

	if strings.Count(imageView, recommendedTag) != 1 {
		t.Fatalf("image recommendations:\n%s", imageView)
	}
}

// A hand-edited config can name an image buildbox cannot set up: the picker
// recommends the default instead, and the text field refuses it.
func TestImageStepKeepsToUbuntu(t *testing.T) {
	t.Parallel()

	offers := []hetzner.ImageOffer{{Name: config.DefaultImage, Detail: "Ubuntu 26.04"}}
	cat := hetzner.Catalog{Images: offers}

	if kept := ensureImage(offers, "debian-13"); len(kept) != 1 {
		t.Fatalf("offered a Debian image: %+v", kept)
	}

	if got := preferredImage(cat, "debian-13"); got != config.DefaultImage {
		t.Fatalf("preferred %q", got)
	}

	if kept := ensureImage(offers, "ubuntu-24.04"); len(kept) != 2 || kept[0].Name != "ubuntu-24.04" {
		t.Fatalf("dropped a configured Ubuntu image: %+v", kept)
	}

	if got := preferredImage(hetzner.Catalog{}, "debian-13"); got != config.DefaultImage {
		t.Fatalf("preferred %q with no catalog", got)
	}

	wiz := tailnetScreen(stepImage, adminTailnet())

	if problem := wiz.commitPlainProblem("debian-13"); !strings.Contains(problem, "Ubuntu") {
		t.Fatalf("debian problem %q", problem)
	}

	if problem := wiz.commitPlainProblem("ubuntu-24.04"); problem != "" {
		t.Fatal(problem)
	}
}

// commitPlainProblem is the validation message for value on the current
// text step.
func (wiz Model) commitPlainProblem(value string) string {
	_, problem := wiz.commitText(value)

	return problem
}

// tailnetScreen opens one step directly. The manual steps imply the manual
// access choice, as they would after the access step.
func tailnetScreen(step stepID, tailnet tailscale.Tailnet) Model {
	wiz := New(config.Config{}, testDest, false, tailnet, nil)
	wiz.step = step
	wiz.width = 88
	wiz.height = 36

	// Opened directly, as on a later run: a first run's screens are
	// reached through the welcome, and firstrun_test covers them.
	wiz.guided = false

	if step == stepPolicy || step == stepKeys || step == stepOAuthID || step == stepOAuthSecret {
		wiz.accessChoice = accessManual
	}

	next, _ := wiz.prepareStep()

	return next
}

func TestAccessStepRecommendsTheToken(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepAccess, adminTailnet())
	view := stripANSI(wiz.View())
	assertViewContains(t, view, accessStepTitle, "Let buildbox set up the tailnet  "+recommendedTag,
		"Set up the tailnet by hand", "Tailnet · 1 of 3")

	if wiz.accessChoice != accessToken {
		t.Fatalf("choice %q", wiz.accessChoice)
	}

	next, _ := wiz.advance()
	if next.step != stepToken || !next.typing() || next.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("token step %d typing %v echo %v", next.step, next.typing(), next.input.EchoMode)
	}

	manual := wiz.selectID(accessManual)

	next, _ = manual.advance()
	if next.step != stepPolicy || next.progress() != "Tailnet · 2 of 5" {
		t.Fatalf("manual step %d progress %q", next.step, next.progress())
	}

	// Esc on a section's first step returns to the hub, on that section.
	hub, _ := wiz.back()
	if hub.step != stepHub || hub.hubChoice != hubTailnet || hub.quit {
		t.Fatalf("back: step %d cursor %q quit %v", hub.step, hub.hubChoice, hub.quit)
	}
}

func TestTokenStepPlansTheSetup(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepToken, adminTailnet())
	wiz.input.SetValue(testAccessToken)

	checking, cmd := wiz.onText(tea.KeyMsg{Type: tea.KeyEnter})
	if !checking.checking || cmd == nil || checking.token != testAccessToken {
		t.Fatalf("checking %v cmd %v token %q", checking.checking, cmd, checking.token)
	}

	view := stripANSI(checking.View())
	if strings.Contains(view, testAccessToken) || !strings.Contains(view, "Reading") {
		t.Fatalf("token view:\n%s", view)
	}
}

func TestPlanResultDecidesTheNextStep(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepToken, adminTailnet())
	wiz.token = testAccessToken
	wiz.checking = true

	refused, _ := wiz.settlePlan(setupPlanned{err: abort.New("The Tailscale API refused the access token")})
	if refused.checking || refused.step != stepToken || !strings.Contains(refused.note, "refused") {
		t.Fatalf("refused: %v %d %q", refused.checking, refused.step, refused.note)
	}

	planned, _ := wiz.settlePlan(setupPlanned{plan: samplePlan()})
	if planned.step != stepPreview || planned.progress() != "Tailnet · 3 of 3" {
		t.Fatalf("planned step %d progress %q", planned.step, planned.progress())
	}
}

func TestPreviewShowsTheChanges(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepPreview, adminTailnet())
	wiz.plan = samplePlan()

	view := stripANSI(wiz.View())
	assertViewContains(t, view, previewStepTitle, "+ acls", "+ ssh", "tagOwners: "+presentMark,
		toolChecked+" Change", "+ OAuth client for tag:buildbox", "space toggle")

	// A rule of buildbox's shape that lacks a user is amended, and says so.
	wiz.plan.Changes[2].Adds = []string{"admin"}

	assertViewContains(t, stripANSI(wiz.View()), "~ ssh: adds admin to the rule")

	toggled, _ := wiz.onPreview(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if toggled.narrow || !strings.Contains(stripANSI(toggled.View()), toolUnchecked+" Change") {
		t.Fatalf("narrow still %v", toggled.narrow)
	}

	applying, cmd := wiz.onPreview(tea.KeyMsg{Type: tea.KeyEnter})
	if !applying.checking || cmd == nil {
		t.Fatal("enter should start applying")
	}
}

func TestApplyResultDecidesTheNextStep(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepPreview, adminTailnet())
	wiz.plan = samplePlan()
	wiz.token = testAccessToken
	wiz.checking = true

	failed, _ := wiz.settleApply(setupApplied{err: abort.New("Tailscale rejected the new policy")})
	if failed.checking || failed.step != stepPreview || !strings.Contains(failed.note, "rejected") {
		t.Fatalf("failed: %v %d %q", failed.checking, failed.step, failed.note)
	}

	done, _ := wiz.settleApply(setupApplied{client: tailscale.OAuthClient{ID: "k1", Secret: testSecret}})
	if done.step != stepHub || !done.MintsKeys() || done.token != "" || !done.setUp || !done.tailnetDecided {
		t.Fatalf("done: step %d mints %v token %q", done.step, done.MintsKeys(), done.token)
	}

	assertViewContains(t, stripANSI(done.View()), "policy updated, OAuth client ready")
}

func samplePlan() tailscale.SetupPlan {
	return tailscale.SetupPlan{
		StockRule: 0,
		Changes: []tailscale.Change{
			{Section: "tagOwners", Line: `"tag:buildbox": ["autogroup:owner"],`, Present: true},
			{Section: "acls", Line: `{"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:buildbox:*"]},`},
			{Section: "ssh", Line: tailscale.SSHRule(config.DefaultTag)},
		},
	}
}

func TestWorkingClientFromTheConfigMarksTheTailnetDone(t *testing.T) {
	t.Parallel()

	seed := config.Config{OAuthClientID: "k1", OAuthClientSecret: testSecret}

	wiz := New(seed, testDest, true, adminTailnet(), nil)
	if !wiz.seedPending {
		t.Fatal("no check planned for the client in the config")
	}

	// The loading screen has been up for its minimum.
	wiz.drift = minLoadTicks

	// The check answers first: the form still waits for the catalog.
	checked, _ := wiz.onSeedChecked(seedChecked{})
	if checked.step != stepLoad || !checked.tailnetDecided {
		t.Fatalf("step %d decided %v", checked.step, checked.tailnetDecided)
	}

	ready, _ := checked.onReady(catalogReady{cat: loadedCatalog()})

	done, ok := ready.(Model)
	if !ok || done.step != stepHub || done.hubChoice != hubAgent || done.progress() != "" || !done.MintsKeys() {
		t.Fatalf("step %d cursor %q progress %q mints %v", done.step, done.hubChoice, done.progress(), done.MintsKeys())
	}

	if quit, _ := done.back(); !quit.quit {
		t.Fatal("esc on the hub should quit")
	}

	assertViewContains(t, fold(stripANSI(done.View())), "OAuth client ready",
		"provider Hetzner type "+config.DefaultType+" location Falkenstein, Germany image ubuntu-26.04 "+
			"price €0.0080 per hour (max €4.99 per month)")
}

// The tailnet section stays open after a working client was found, for a
// repair, and its first screen says so.
func TestTailnetSectionStaysOpenForARepair(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepHub, adminTailnet())
	wiz.oauthID = "k1"
	wiz.oauthSecret = testSecret
	wiz.tailnetDecided = true
	wiz.hubChoice = hubTailnet

	again, _ := wiz.onHub(tea.KeyMsg{Type: tea.KeyEnter})
	if again.step != stepAccess || again.progress() != "Tailnet · 1 of 3" {
		t.Fatalf("step %d progress %q", again.step, again.progress())
	}

	assertViewContains(t, fold(stripANSI(again.View())), "example.com is set up")
}

func TestBrokenClientFromTheConfigLeavesTheTailnetUndone(t *testing.T) {
	t.Parallel()

	seed := config.Config{OAuthClientID: "k1", OAuthClientSecret: testSecret}
	wiz := New(seed, testDest, true, adminTailnet(), nil)
	wiz.drift = minLoadTicks

	// The catalog answers first: the form still waits for the check.
	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	loading, ok := ready.(Model)
	if !ok || loading.step != stepLoad {
		t.Fatalf("step %d", loading.step)
	}

	next, _ := loading.onSeedChecked(seedChecked{err: abort.New("The Tailscale API refused the OAuth client")})
	if next.step != stepHub || next.tailnetDecided || next.MintsKeys() {
		t.Fatalf("step %d decided %v mints %v", next.step, next.tailnetDecided, next.MintsKeys())
	}

	next.width = 160
	next.height = 36

	assertViewContains(t, fold(stripANSI(next.View())), "refused the OAuth client", "new OAuth client is made here",
		"not set up yet")
}

func TestNoClientInTheConfigNeedsNoCheck(t *testing.T) {
	t.Parallel()

	wiz := New(config.Config{}, testDest, false, adminTailnet(), nil)
	if wiz.seedPending {
		t.Fatal("a check planned without a client")
	}

	wiz.drift = minLoadTicks

	ready, _ := wiz.onReady(catalogReady{cat: loadedCatalog()})

	// A first run waits on its welcome, ready to begin.
	next, ok := ready.(Model)
	if !ok || next.step != stepLoad || !next.ready() {
		t.Fatalf("step %d ready %v", next.step, next.ready())
	}

	assertViewContains(t, stripANSI(next.View()), welcomeKeys)
}

func TestHubCursorFollowsWhatIsMissing(t *testing.T) {
	t.Parallel()

	wiz := completeModel(t)
	if wiz.hubCursor() != hubTailnet || len(wiz.writeBlockers()) != 1 {
		t.Fatalf("cursor %q blockers %v", wiz.hubCursor(), wiz.writeBlockers())
	}

	wiz.tailnetDecided = true
	if wiz.hubCursor() != hubWrite || len(wiz.writeBlockers()) != 0 {
		t.Fatalf("cursor %q blockers %v", wiz.hubCursor(), wiz.writeBlockers())
	}

	// Arriving with nothing missing starts at the top, not on Write.
	wiz.loaded = true
	wiz.drift = minLoadTicks

	arrived, _ := wiz.leaveLoad()
	if arrived.hubChoice != hubBox {
		t.Fatalf("arrival cursor %q", arrived.hubChoice)
	}

	assertViewContains(t, stripANSI(wiz.View()), "Write"+strings.Repeat(" ", hubLabelWidth-len("Write"))+
		"replaces "+filepath.Base(testDest))
}

func TestSectionsReturnToTheHub(t *testing.T) {
	t.Parallel()

	wiz := completeModel(t)
	wiz.tailnetDecided = true
	wiz.hubChoice = hubBox

	box, _ := wiz.onHub(tea.KeyMsg{Type: tea.KeyEnter})
	if box.step != stepLocation || box.progress() != "Box · 1 of 3" {
		t.Fatalf("step %d progress %q", box.step, box.progress())
	}

	machine, _ := box.advance()

	last, _ := machine.advance()
	if last.step != stepImage || last.progress() != "Box · 3 of 3" {
		t.Fatalf("step %d progress %q", last.step, last.progress())
	}

	hub, _ := last.advance()
	if hub.step != stepHub || hub.hubChoice != hubWrite {
		t.Fatalf("step %d cursor %q", hub.step, hub.hubChoice)
	}

	assertViewContains(t, stripANSI(hub.View()), "enter write")
}

func TestHubRowsContinueBelowOnANarrowTerminal(t *testing.T) {
	t.Parallel()

	wiz := completeModel(t)
	wiz.width = 60
	wiz.height = 30

	view := stripANSI(wiz.View())
	if strings.Contains(view, "…") {
		t.Fatalf("a hub row was cut:\n%s", view)
	}

	assertViewContains(t, fold(view), "provider Hetzner type "+config.DefaultType+" location Falkenstein, Germany",
		"price €0.0080 per hour (max €4.99 per month)", "git name "+namePlaceholder+" git email "+emailPlaceholder,
		"git email "+emailPlaceholder+" tools OpenCode")
}

func TestWriteWaitsForTheSections(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepHub, adminTailnet())
	wiz.hubChoice = hubWrite

	view := stripANSI(wiz.View())
	assertViewContains(t, view, "after Box, Agent, and Tailnet", "enter open")

	blocked, cmd := wiz.onHub(tea.KeyMsg{Type: tea.KeyEnter})
	if blocked.confirmed || cmd != nil || !strings.Contains(blocked.note, "Finish") {
		t.Fatalf("confirmed %v note %q", blocked.confirmed, blocked.note)
	}

	ready := completeModel(t)
	ready.tailnetDecided = true
	ready.hubChoice = hubWrite

	written, cmd := ready.onHub(tea.KeyMsg{Type: tea.KeyEnter})
	if !written.confirmed || cmd == nil {
		t.Fatal("enter on a ready Write should confirm")
	}
}

func TestIdentityStepMovesFromNameToEmail(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepGitIdentity, adminTailnet())
	if !wiz.typing() || wiz.focusEmail || wiz.progress() != "Agent · 2 of 3" {
		t.Fatalf("typing %v email %v progress %q", wiz.typing(), wiz.focusEmail, wiz.progress())
	}

	assertViewContains(t, stripANSI(wiz.View()), identityStepTitle, "Name", "Email", nameFooter)

	refused, _ := wiz.onIdentity(tea.KeyMsg{Type: tea.KeyEnter})
	if refused.focusEmail || refused.note == "" {
		t.Fatalf("an empty name should be refused: %q", refused.note)
	}

	wiz.input.SetValue(namePlaceholder)

	onEmail, _ := wiz.onIdentity(tea.KeyMsg{Type: tea.KeyTab})
	if !onEmail.focusEmail || onEmail.gitName != namePlaceholder || onEmail.note != "" {
		t.Fatalf("email %v name %q note %q", onEmail.focusEmail, onEmail.gitName, onEmail.note)
	}

	assertViewContains(t, stripANSI(onEmail.View()), emailFooter)

	back, _ := onEmail.onIdentity(tea.KeyMsg{Type: tea.KeyShiftTab})
	if back.focusEmail {
		t.Fatal("shift+tab should return to the name")
	}
}

func TestIdentityStepChecksTheEmail(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepGitIdentity, adminTailnet())
	wiz.input.SetValue(namePlaceholder)

	onEmail, _ := wiz.onIdentity(tea.KeyMsg{Type: tea.KeyEnter})
	onEmail.email.SetValue("not-an-email")

	bad, _ := onEmail.onIdentity(tea.KeyMsg{Type: tea.KeyEnter})
	if bad.step != stepGitIdentity || bad.note == "" {
		t.Fatalf("a bad email should be refused: step %d note %q", bad.step, bad.note)
	}

	onEmail.email.SetValue(emailPlaceholder)

	next, _ := onEmail.onIdentity(tea.KeyMsg{Type: tea.KeyEnter})
	if next.step != stepTools || next.gitEmail != emailPlaceholder {
		t.Fatalf("step %d email %q", next.step, next.gitEmail)
	}
}

func TestRowDescriptionsWrapInsteadOfBeingCut(t *testing.T) {
	t.Parallel()

	wiz := tailnetScreen(stepAccess, adminTailnet())
	wiz.width = 80
	wiz.height = 24

	view := stripANSI(wiz.View())
	if strings.Contains(view, "…") {
		t.Fatalf("a description was cut:\n%s", view)
	}

	assertViewContains(t, fold(view), "discards the token.", "at every create.")
}

// completeModel has every section but the tailnet done: a real key file, an
// identity, and a catalog with the seed location and type.
func completeModel(t *testing.T) Model {
	t.Helper()

	key := filepath.Join(t.TempDir(), "git_ed25519")

	err := os.WriteFile(key, []byte("private\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	seed := config.Config{
		GitSSHKeyPath: key,
		GitUserName:   namePlaceholder,
		GitUserEmail:  emailPlaceholder,
		AITools:       config.ToolOpenCode,
		ServerLoc:     config.SeedLocation,
		ServerType:    config.DefaultType,
		ServerImage:   config.DefaultImage,
	}

	wiz := New(seed, testDest, true, adminTailnet(), nil)
	wiz.cat = loadedCatalog()
	wiz.step = stepHub
	wiz.width = 88
	wiz.height = 36

	return wiz
}

// loadedCatalog is the least the form needs to leave the loading screen.
func loadedCatalog() hetzner.Catalog {
	return hetzner.Catalog{
		Currency:  testCurrency,
		Locations: []hetzner.LocationOffer{falkenstein(2, 0)},
		Machines:  []hetzner.MachineOffer{machine(config.DefaultType, true, true, "0.0080000000", "4.9900000000")},
	}
}
