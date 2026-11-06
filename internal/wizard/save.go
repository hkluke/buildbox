package wizard

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
)

const quitQuestion = "Quit without writing? Unsaved changes are lost.  y quit  ·  any other key stays"

// clientSaved reports the OAuth client written to the credentials file
// before Write. Its secret is shown only once, so it is saved the moment it
// exists rather than lost if init ends early.
type clientSaved struct {
	path   string
	id     string
	secret string
	err    error
}

func saveClient(path string, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		_, err := config.WriteCredentials(path, cfg)

		return clientSaved{path: path, id: cfg.OAuthClientID, secret: cfg.OAuthClientSecret, err: err}
	}
}

// keepClient saves the OAuth client the form now has, alongside the step's
// own command.
func (wiz Model) keepClient(next Model, cmd tea.Cmd) (Model, tea.Cmd) {
	return next, tea.Batch(cmd, saveClient(config.CredentialsPath(wiz.dest), wiz.Config()))
}

func (wiz Model) onClientSaved(msg clientSaved) Model {
	if msg.err != nil {
		wiz.note = "Could not save the OAuth client yet: " + strings.Join(strings.Fields(msg.err.Error()), " ") +
			" Write saves it."

		return wiz
	}

	wiz.saved.OAuthClientID = msg.id
	wiz.saved.OAuthClientSecret = msg.secret
	wiz.note = "The OAuth client is saved in " + console.DisplayPath(msg.path) + "."

	return wiz
}

// unsaved is whether quitting now would lose something chosen in this run:
// a setting that differs from what init started with, or a new Git key whose
// public half is only shown after Write.
func (wiz Model) unsaved() bool {
	return wiz.Config() != wiz.saved || wiz.gitPublicKey != ""
}

// requestQuit leaves init, asking first when something would be lost.
// Before the loading screen or the welcome is left, nothing can be.
func (wiz Model) requestQuit() (Model, tea.Cmd) {
	if wiz.begun && !wiz.askingQuit && wiz.unsaved() {
		wiz.askingQuit = true

		return wiz, nil
	}

	wiz.quit = true

	return wiz, tea.Quit
}

// answerQuit takes the key pressed at the question: y or a second ctrl+c
// quits, anything else stays.
func (wiz Model) answerQuit(msg tea.KeyMsg) (Model, tea.Cmd) {
	wiz.askingQuit = false

	if msg.String() == "y" || msg.String() == "ctrl+c" {
		wiz.quit = true

		return wiz, tea.Quit
	}

	return wiz, nil
}
