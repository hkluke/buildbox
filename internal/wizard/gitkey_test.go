package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/openssh"
)

// The screen asks one question, whether boxes share a Git key, and uses the
// same few words for it as create does later: Git key, box key, public key.
func TestGitKeyChoiceSuggestsASharedKey(t *testing.T) {
	t.Parallel()

	wiz := gitScreen(filepath.Join(t.TempDir(), "missing"))
	if wiz.gitSuggest != gitChoiceNew || wiz.gitChoice != gitChoiceNew {
		t.Fatalf("suggest %q choice %q", wiz.gitSuggest, wiz.gitChoice)
	}

	view := stripANSI(wiz.View())

	assertViewContains(
		t,
		view,
		gitStepTitle,
		"How boxes log in to GitHub and GitLab.",
		"Generate a shared Git key  "+recommendedTag,
		"You add it to GitHub or GitLab once. Every new box can push right away.",
		customPathTitle,
		"Type the private key path. Every box gets a copy of it.",
		noGitKeyTitle,
		"Each box uses its own box key. You add that key for every new box.",
	)

	if strings.Count(view, recommendedTag) != 1 || strings.Contains(view, foundMeta) {
		t.Fatalf("choices:\n%s", view)
	}

	for _, retired := range []string{"dedicated", "not yours", "public half", "agent's"} {
		if strings.Contains(view, retired) {
			t.Fatalf("%q is back on the screen:\n%s", retired, view)
		}
	}
}

// A key init comes across on a first run is most likely the user's own, and
// every box would get a copy of it. It is offered, and a new key stays the
// recommendation.
func TestAKeyFoundOnAFirstRunIsOfferedNotRecommended(t *testing.T) {
	t.Parallel()

	private := existingKey(t)

	wiz := gitScreen(private)
	if wiz.gitSuggest != gitChoiceNew || wiz.gitChoice != gitChoiceNew {
		t.Fatalf("suggest %q choice %q", wiz.gitSuggest, wiz.gitChoice)
	}

	view := stripANSI(wiz.View())
	assertViewContains(t, view, "Generate a shared Git key  "+recommendedTag, foundMeta, " as the shared Git key")

	if strings.Count(view, recommendedTag) != 1 || wiz.gitUseRow().tag != "" {
		t.Fatalf("a found key must not be recommended:\n%s", view)
	}

	if strings.Index(view, foundMeta) < strings.Index(view, "Generate a shared Git key") {
		t.Fatalf("the recommended choice should come first:\n%s", view)
	}

	wiz.gitChoice = gitChoiceUse

	kept, _ := wiz.confirmGit()
	if kept.gitKey != private {
		t.Fatalf("choosing the found key stored %q", kept.gitKey)
	}
}

// On a later run the key the config names is the user's own earlier answer,
// so it comes first and is the one recommended.
func TestTheKeyAConfigNamesStaysRecommended(t *testing.T) {
	t.Parallel()

	private := existingKey(t)

	wiz := gitScreenOn(private, true)
	if wiz.gitSuggest != gitChoiceUse || wiz.gitChoice != gitChoiceUse {
		t.Fatalf("suggest %q choice %q", wiz.gitSuggest, wiz.gitChoice)
	}

	view := stripANSI(wiz.View())
	assertViewContains(t, view, foundMeta, "Generate a shared Git key", customPathTitle, noGitKeyTitle)

	if wiz.gitKey != private {
		t.Fatalf("stored %q", wiz.gitKey)
	}

	if strings.Count(view, recommendedTag) != 1 || wiz.gitNewRow().tag != "" {
		t.Fatalf("the configured key should be the only recommended choice:\n%s", view)
	}

	if strings.Index(view, foundMeta) > strings.Index(view, "Generate a shared Git key") {
		t.Fatalf("the configured key should come first:\n%s", view)
	}
}

// Sharing no Git key is an answer, not a gap: the config says so in a word,
// the overview has nothing left to ask for, and a later run opens on it.
func TestNoSharedGitKeyIsAnAnswer(t *testing.T) {
	t.Parallel()

	wiz := gitScreen(filepath.Join(t.TempDir(), "missing"))
	wiz.gitPublicKey = "ssh-ed25519 AAAA a key generated earlier in this run"
	wiz.gitChoice = gitChoiceNone

	chosen, _ := wiz.confirmGit()
	if chosen.gitKey != config.GitKeyNone || chosen.Config().GitSSHKeyPath != config.GitKeyNone {
		t.Fatalf("stored %q", chosen.gitKey)
	}

	if chosen.NewGitPublicKey() != "" {
		t.Fatal("a key that boxes will not share is still to be shown at the end")
	}

	chosen.gitName, chosen.gitEmail = "Ada Example", "ada@example.com"

	if problem := chosen.agentProblem(); problem != "" {
		t.Fatalf("the Agent section still asks for something: %q", problem)
	}

	if chosen.gitKeyFact() != noGitKeyFact {
		t.Fatalf("overview shows %q", chosen.gitKeyFact())
	}

	later := gitScreenOn(config.GitKeyNone, true)
	if later.gitChoice != gitChoiceNone || later.gitPath() != "" {
		t.Fatalf("a later run opens on %q with path %q", later.gitChoice, later.gitPath())
	}

	view := stripANSI(later.View())
	if strings.Contains(view, foundMeta) || strings.Contains(view, "Use none") {
		t.Fatalf("none was offered as a key:\n%s", view)
	}
}

func TestGitCustomPathOpensTheEditor(t *testing.T) {
	t.Parallel()

	wiz := gitScreen(filepath.Join(t.TempDir(), "missing"))
	wiz.gitChoice = gitChoicePath

	editing, _ := wiz.confirmGit()
	if !editing.gitEditing {
		t.Fatal("custom path should open the path editor")
	}

	assertViewContains(t, stripANSI(editing.View()), "Path to the private key.", "already be on GitHub or GitLab")

	back, _ := editing.back()
	if back.gitEditing || back.gitChoice != gitChoicePath {
		t.Fatalf("editing %v choice %q", back.gitEditing, back.gitChoice)
	}

	if !strings.Contains(stripANSI(back.View()), customPathTitle) {
		t.Fatal("esc should return to the Git key choices")
	}
}

func TestGitPathMustBeAPrivateKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	public := filepath.Join(dir, "key.pub")

	err := os.WriteFile(public, []byte("ssh-ed25519 AAAA test\n"), openssh.FilePerm)
	if err != nil {
		t.Fatal(err)
	}

	wiz := gitScreen(filepath.Join(dir, "missing"))

	_, problem := wiz.commitPath(public)
	if !strings.Contains(problem, "public key") {
		t.Fatalf("problem %q", problem)
	}
}

func TestGenerateGitKeyNamesItForTheGitHost(t *testing.T) {
	t.Parallel()

	private := filepath.Join(t.TempDir(), "buildbox_git_ed25519")

	key, problem := generateGitKey(private)
	if problem != "" {
		t.Fatal(problem)
	}

	if key.private != private {
		t.Fatalf("private %q", key.private)
	}

	if !strings.HasPrefix(key.public, "ssh-ed25519 ") || !strings.HasSuffix(key.public, " "+gitKeyComment) {
		t.Fatalf("public key %q", key.public)
	}

	if strings.Contains(key.public, "\n") {
		t.Fatalf("public key should be one line %q", key.public)
	}

	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != openssh.FilePerm {
		t.Fatalf("mode %o", info.Mode().Perm())
	}

	_, problem = generateGitKey(private)
	if !strings.Contains(problem, "already exists") {
		t.Fatalf("problem %q", problem)
	}
}

// gitScreen is the Git key screen of a first run.
func gitScreen(path string) Model {
	return gitScreenOn(path, false)
}

// existingKey is a private key file on this machine.
func existingKey(t *testing.T) string {
	t.Helper()

	private := filepath.Join(t.TempDir(), "github_ed25519")

	err := os.WriteFile(private, []byte("private\n"), openssh.FilePerm)
	if err != nil {
		t.Fatal(err)
	}

	return private
}

// gitScreenOn is the Git key screen with a key in the form. replacing is
// whether that key comes from a config an earlier run wrote.
func gitScreenOn(path string, replacing bool) Model {
	wiz := New(config.Config{GitSSHKeyPath: path}, testDest, replacing, adminTailnet(), nil)
	wiz.step = stepGitKey
	wiz.width = 88
	wiz.height = 32

	next, _ := wiz.openGitChoice(true)

	return next
}

// fold joins a rendered view into one line of single spaces, so a sentence
// can be checked across the line breaks of a wrapped hint.
func fold(view string) string {
	return strings.Join(strings.Fields(view), " ")
}
