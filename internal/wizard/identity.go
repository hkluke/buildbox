package wizard

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hkluke/buildbox/internal/config"
	"github.com/hkluke/buildbox/internal/console"
)

// The Git identity step has two fields on one screen, since the name and the
// email are always set together.
const (
	identityStepTitle = "Git identity"
	namePlaceholder   = "Ada Lovelace"
	emailPlaceholder  = "ada@example.com"
	nameFooter        = "enter next  ·  esc back  ·  ctrl+c quit"
	emailFooter       = "enter continue  ·  shift+tab name  ·  esc back  ·  ctrl+c quit"
	identityChrome    = 4
)

// newField is a text field in the form's colors.
func newField(paint console.Paint) textinput.Model {
	field := textinput.New()
	field.Prompt = ""
	field.CharLimit = pathFieldLimit
	field.PlaceholderStyle = paint.Faint
	field.TextStyle = paint.Text
	field.Cursor.Style = paint.Accent

	return field
}

// prepareIdentity fills both fields and puts the cursor on the name.
func (wiz Model) prepareIdentity() (Model, tea.Cmd) {
	wiz.focusEmail = false

	wiz.input.EchoMode = textinput.EchoNormal
	wiz.input.SetValue(wiz.gitName)
	wiz.input.Placeholder = namePlaceholder
	wiz.input.CursorEnd()
	wiz.input.Width = wiz.fieldWidth()

	wiz.email.SetValue(wiz.gitEmail)
	wiz.email.Placeholder = emailPlaceholder
	wiz.email.CursorEnd()
	wiz.email.Width = wiz.fieldWidth()
	wiz.email.Blur()

	return wiz, wiz.input.Focus()
}

// onIdentity runs the two fields. Enter and tab move from the name to the
// email; enter on the email stores both and moves on.
func (wiz Model) onIdentity(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter, "tab", "down":
		return wiz.commitIdentityField()
	case "shift+tab", "up":
		if wiz.focusEmail {
			return wiz.focusNameField()
		}

		return wiz, nil
	default:
		return wiz.editIdentityField(msg)
	}
}

func (wiz Model) commitIdentityField() (Model, tea.Cmd) {
	if !wiz.focusEmail {
		name := strings.TrimSpace(wiz.input.Value())
		if problem := config.InvalidPersonName(name); problem != "" {
			wiz.note = problem

			return wiz, nil
		}

		wiz.gitName = name
		wiz.focusEmail = true
		wiz.note = ""
		wiz.input.Blur()

		return wiz, wiz.email.Focus()
	}

	email := strings.TrimSpace(wiz.email.Value())
	if problem := config.InvalidEmail(email); problem != "" {
		wiz.note = problem

		return wiz, nil
	}

	wiz.gitEmail = email

	return wiz.advance()
}

func (wiz Model) focusNameField() (Model, tea.Cmd) {
	wiz.focusEmail = false
	wiz.note = ""
	wiz.email.Blur()

	return wiz, wiz.input.Focus()
}

func (wiz Model) editIdentityField(msg tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd

	wiz.note = ""

	if wiz.focusEmail {
		wiz.email, cmd = wiz.email.Update(msg)

		return wiz, cmd
	}

	wiz.input, cmd = wiz.input.Update(msg)

	return wiz, cmd
}

func (wiz Model) identityFooter() string {
	if wiz.focusEmail {
		return emailFooter
	}

	return nameFooter
}

// viewIdentity shows the two fields; the rule under the one being edited is
// in the accent color.
func (wiz Model) viewIdentity() string {
	title, hint := wiz.question()
	name := wiz.fieldBlock("Name", wiz.input, !wiz.focusEmail)
	email := wiz.fieldBlock("Email", wiz.email, wiz.focusEmail)

	parts := make([]string, 0, len(name)+len(email)+identityChrome)
	parts = append(parts, wiz.viewHeader(title, hint))
	parts = append(parts, name...)
	parts = append(parts, "")
	parts = append(parts, email...)
	parts = append(parts, "", wiz.viewFooter())

	return wiz.screen(strings.Join(parts, "\n"))
}

func (wiz Model) fieldBlock(label string, field textinput.Model, focused bool) []string {
	view := field.View()
	width := min(max(lipgloss.Width(view), minFieldDraw), wiz.innerWidth())

	rule := wiz.paint.Rule
	if focused {
		rule = wiz.paint.Accent
	}

	return []string{wiz.paint.Muted.Render(label), view, rule.Render(strings.Repeat("─", width))}
}
