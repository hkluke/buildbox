package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/openssh"
)

func TestGitKeyChoiceSuggestsADedicatedKey(t *testing.T) {
	t.Parallel()

	wiz := gitScreen(filepath.Join(t.TempDir(), "missing"))
	if wiz.gitSuggest != gitChoiceNew || wiz.gitChoice != gitChoiceNew {
		t.Fatalf("suggest %q choice %q", wiz.gitSuggest, wiz.gitChoice)
	}

	view := stripANSI(wiz.View())

	// The hint wraps, so its sentences are checked with the line breaks folded.
	prose := fold(view)
	assertViewContains(
		t,
		prose,
		"GitHub or GitLab",
		"copied to the box",
		"a key of its own, not yours",
		"shown at the end",
	)
	assertViewContains(
		t,
		view,
		gitStepTitle,
		"Generate a dedicated key  "+recommendedTag,
		"ed25519, no passphrase",
		customPathTitle,
		"Type the private key path.",
	)

	if strings.Count(view, recommendedTag) != 1 || strings.Contains(view, foundMeta) {
		t.Fatalf("choices:\n%s", view)
	}
}

func TestGitKeyChoicePrefersExistingKey(t *testing.T) {
	t.Parallel()

	private := filepath.Join(t.TempDir(), "github_ed25519")

	err := os.WriteFile(private, []byte("private\n"), openssh.FilePerm)
	if err != nil {
		t.Fatal(err)
	}

	wiz := gitScreen(private)
	if wiz.gitSuggest != gitChoiceUse || wiz.gitChoice != gitChoiceUse {
		t.Fatalf("suggest %q choice %q", wiz.gitSuggest, wiz.gitChoice)
	}

	view := stripANSI(wiz.View())
	assertViewContains(t, view, foundMeta, "Generate a dedicated key", customPathTitle)

	if wiz.gitKey != private {
		t.Fatalf("stored %q", wiz.gitKey)
	}

	if strings.Count(view, recommendedTag) != 1 || wiz.gitNewRow().tag != "" {
		t.Fatalf("the existing key should be the only recommended choice:\n%s", view)
	}

	if strings.Index(view, foundMeta) > strings.Index(view, "Generate a dedicated key") {
		t.Fatalf("the existing key should come first:\n%s", view)
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

func gitScreen(path string) Model {
	wiz := New(config.Config{GitSSHKeyPath: path}, testDest, false, adminTailnet(), nil)
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
