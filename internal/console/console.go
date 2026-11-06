// Package console is how buildbox writes to the terminal: the checklist every
// command prints, the palettes, failures with their hints, and help styling.
// Work inside a step never prints; it returns a result or an error.
package console

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/hkluke/buildbox/internal/abort"
)

// StepIndent starts every line of the checklist and of a failure's hint.
const StepIndent = "  "

const (
	stepLabelWidth  = 12
	stepDetailWidth = 30
	elapsedWidth    = 7
	hintWidth       = 76
	compactLine     = stepLabelWidth + elapsedWidth + 8
	detailIndent    = "    "
	commandGap      = "   "
	spinFrames      = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	spinInterval    = 100 * time.Millisecond
	iconDone        = "✓"
	iconFailed      = "✗"
	iconSkipped     = "·"
	iconWaiting     = "…"
	clearLine       = "\r\033[K"
)

var helpSection = regexp.MustCompile(`^[A-Z][A-Za-z ]*:$`)

// Paint is a palette: one style for each kind of text.
type Paint struct {
	Title  lipgloss.Style
	Muted  lipgloss.Style
	Faint  lipgloss.Style
	Accent lipgloss.Style
	Code   lipgloss.Style
	Warn   lipgloss.Style
	Fail   lipgloss.Style
	OK     lipgloss.Style
	Text   lipgloss.Style
	Rule   lipgloss.Style
}

// AdaptivePaint is the init wizard's palette. Its adaptive colors make
// lipgloss ask the terminal for its background once, which a full-screen
// flow can afford.
func AdaptivePaint() Paint {
	brass := lipgloss.AdaptiveColor{Light: "#8A5A22", Dark: "#E0B15A"}
	moss := lipgloss.AdaptiveColor{Light: "#1F7A4D", Dark: "#8FCBB0"}
	ink := lipgloss.AdaptiveColor{Light: "#1C1917", Dark: "#F5F5F4"}
	stone := lipgloss.AdaptiveColor{Light: "#57534E", Dark: "#A8A29E"}
	ash := lipgloss.AdaptiveColor{Light: "#A8A29E", Dark: "#78716C"}
	clay := lipgloss.AdaptiveColor{Light: "#9A3412", Dark: "#E7B089"}
	rust := lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#F28B82"}
	line := lipgloss.AdaptiveColor{Light: "#D6D3D1", Dark: "#44403C"}

	return Paint{
		Title:  lipgloss.NewStyle().Foreground(ink).Bold(true),
		Muted:  lipgloss.NewStyle().Foreground(stone),
		Faint:  lipgloss.NewStyle().Foreground(ash),
		Accent: lipgloss.NewStyle().Foreground(brass).Bold(true),
		Code:   lipgloss.NewStyle().Foreground(brass),
		Warn:   lipgloss.NewStyle().Foreground(clay),
		Fail:   lipgloss.NewStyle().Foreground(rust).Bold(true),
		OK:     lipgloss.NewStyle().Foreground(moss),
		Text:   lipgloss.NewStyle().Foreground(ink),
		Rule:   lipgloss.NewStyle().Foreground(line),
	}
}

// cliPaint uses the terminal's own 16 colors, which its theme already keeps
// readable on its background. Adaptive colors would query the background on
// every command, and a terminal that never answers stalls each one for 5s.
// The renderer is per output, so a pipe or NO_COLOR turns colors off for
// that output alone.
func cliPaint(renderer *lipgloss.Renderer) Paint {
	yellow := lipgloss.Color("3")

	return Paint{
		Title:  renderer.NewStyle().Bold(true),
		Muted:  renderer.NewStyle().Faint(true),
		Faint:  renderer.NewStyle().Faint(true),
		Accent: renderer.NewStyle().Foreground(yellow).Bold(true),
		Code:   renderer.NewStyle().Foreground(yellow),
		Warn:   renderer.NewStyle().Foreground(yellow),
		Fail:   renderer.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		OK:     renderer.NewStyle().Foreground(lipgloss.Color("2")),
		Text:   renderer.NewStyle(),
		Rule:   renderer.NewStyle().Faint(true),
	}
}

// StdoutPaint is the command palette for text written to stdout.
func StdoutPaint() Paint {
	return cliPaint(lipgloss.NewRenderer(os.Stdout))
}

// StderrPaint is the command palette for text written to stderr.
func StderrPaint() Paint {
	return cliPaint(lipgloss.NewRenderer(os.Stderr))
}

// Console writes command progress to stdout. On a terminal the running step
// redraws in place. In a pipe or a log, a step is one line when it ends, with
// a reminder once a minute while it runs, so a log never shows a spinner.
type Console struct {
	Paint Paint
	out   io.Writer
	live  bool
}

// New returns the console for stdout.
func New() *Console {
	return &Console{
		out:   os.Stdout,
		Paint: StdoutPaint(),
		live:  term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("TERM") != "dumb",
	}
}

// NewWriter returns a console that writes to w as to a log: no spinner, and
// colors only when w is a terminal that wants them.
func NewWriter(w io.Writer) *Console {
	return &Console{out: w, Paint: cliPaint(lipgloss.NewRenderer(w))}
}

// Writeln writes one line as it is.
func (con *Console) Writeln(text string) {
	con.write(text + "\n")
}

// Header names the command and its target, with facts such as the server
// type after it.
func (con *Console) Header(command, name string, facts ...string) {
	line := con.Paint.Title.Render(abort.Prog()+" "+command) + " " + con.Paint.Accent.Render(name)
	if len(facts) > 0 {
		line += con.Paint.Muted.Render(" · " + strings.Join(facts, " · "))
	}

	con.Writeln(line)
	con.Writeln("")
}

// Run shows action as one step. action returns the text shown once it ends.
// A failed step is followed by a blank line, so the error main prints next
// stands apart from the checklist.
func (con *Console) Run(label, running string, action func() (string, error)) error {
	work := con.start(label, running)

	result, err := action()
	if err != nil {
		work.finish(con.Paint.Fail.Render(iconFailed), "failed")
		con.Writeln("")

		return err
	}

	work.finish(con.Paint.OK.Render(iconDone), result)

	return nil
}

// Done prints a step that needed no waiting.
func (con *Console) Done(label, detail string) {
	con.Writeln(con.stepLine(con.Paint.OK.Render(iconDone), label, detail, 0))
}

// Failed prints a step that did not pass and needed no waiting, such as a
// missing requirement. Its reason follows in the error main prints.
func (con *Console) Failed(label, detail string) {
	con.Writeln(con.stepLine(con.Paint.Fail.Render(iconFailed), label, detail, 0))
}

// Skip prints a step that was not done, and why.
func (con *Console) Skip(label, detail string) {
	con.Writeln(StepIndent + con.Paint.Faint.Render(iconSkipped+" "+PadRight(label, stepLabelWidth)+" "+detail))
}

// Blank prints an empty line.
func (con *Console) Blank() {
	con.Writeln("")
}

// Command prints one command to copy, indented like the commands in hints.
func (con *Console) Command(text string) {
	con.Writeln(StepIndent + abort.Indent + con.Paint.Code.Render(text))
}

// Note prints a muted line of prose.
func (con *Console) Note(text string) {
	con.Writeln(StepIndent + con.Paint.Muted.Render(text))
}

// Say prints a result: lead in bold, rest muted.
func (con *Console) Say(lead, rest string) {
	con.Writeln(StepIndent + con.Paint.Title.Render(lead) + con.Paint.Muted.Render(rest))
}

// Commands lists what to run next, aligned on the command.
func (con *Console) Commands(rows [][2]string) {
	width := 0
	for _, row := range rows {
		width = max(width, lipgloss.Width(row[0]))
	}

	for _, row := range rows {
		con.Writeln(StepIndent + con.Paint.Code.Render(PadRight(row[0], width)) + commandGap + con.Paint.Muted.Render(row[1]))
	}
}

// write drops write errors: when stdout is gone, nobody is left to tell.
func (con *Console) write(text string) {
	_, _ = io.WriteString(con.out, text)
}

func (con *Console) stepLine(icon, label, detail string, elapsed time.Duration) string {
	line := StepIndent + icon + " " + con.Paint.Text.Render(PadRight(label, stepLabelWidth)) + " "
	if elapsed < time.Second {
		return line + con.Paint.Muted.Render(detail)
	}

	return line + con.Paint.Muted.Render(PadRight(detail, stepDetailWidth)) +
		con.Paint.Faint.Render(PadLeft(FormatElapsed(elapsed), elapsedWidth))
}

type step struct {
	con     *Console
	label   string
	running string
	started time.Time
	stop    chan struct{}
	stopped chan struct{}
}

func (con *Console) start(label, running string) *step {
	work := &step{
		con:     con,
		label:   label,
		running: running,
		started: time.Now(),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	go work.animate()

	return work
}

func (work *step) animate() {
	defer close(work.stopped)

	ticker := time.NewTicker(spinInterval)
	defer ticker.Stop()

	frames := []rune(spinFrames)
	tick := 0
	reported := time.Duration(0)

	for {
		if work.con.live {
			work.redraw(string(frames[tick%len(frames)]))
		} else {
			reported = work.remind(reported)
		}

		select {
		case <-work.stop:
			return
		case <-ticker.C:
			tick++
		}
	}
}

// redraw falls back to label and time when the full line would wrap, since a
// wrapped line can no longer be redrawn in place. A width of 0 is unknown.
func (work *step) redraw(frame string) {
	icon := work.con.Paint.Accent.Render(frame)
	elapsed := time.Since(work.started)
	line := work.con.stepLine(icon, work.label, work.running, elapsed)

	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err == nil && width > 0 && lipgloss.Width(line) >= width {
		line = StepIndent + icon + " " + work.con.Paint.Text.Render(work.label)
		if width > compactLine {
			line += " " + work.con.Paint.Faint.Render(FormatElapsed(elapsed))
		}
	}

	work.con.write(clearLine + line)
}

func (work *step) remind(reported time.Duration) time.Duration {
	elapsed := time.Since(work.started).Truncate(time.Minute)
	if elapsed <= reported {
		return reported
	}

	label := PadRight(work.label, stepLabelWidth)
	note := fmt.Sprintf("%s %s still running · %dm", iconWaiting, label, int(elapsed.Minutes()))
	work.con.Writeln(StepIndent + work.con.Paint.Faint.Render(note))

	return elapsed
}

func (work *step) finish(icon, detail string) {
	close(work.stop)
	<-work.stopped

	line := work.con.stepLine(icon, work.label, detail, time.Since(work.started))
	if work.con.live {
		work.con.write(clearLine)
	}

	work.con.Writeln(line)
}

// PrintFailure writes err to stderr, with its detail and hint when it is an
// abort.Error.
func PrintFailure(err error) {
	var failed *abort.Error
	if !errors.As(err, &failed) {
		failed = &abort.Error{Headline: err.Error()}
	}

	fmt.Fprint(os.Stderr, renderFailure(StderrPaint(), failed))
}

func renderFailure(paint Paint, failed *abort.Error) string {
	var out strings.Builder

	out.WriteString(paint.Fail.Render(iconFailed) + " " + paint.Title.Render(failed.Headline) + "\n")

	for _, line := range splitLines(strings.TrimSpace(failed.Detail)) {
		out.WriteString(detailIndent + paint.Muted.Render(line) + "\n")
	}

	if failed.Hint != "" {
		out.WriteString("\n")

		for _, line := range splitLines(failed.Hint) {
			out.WriteString(hintLines(paint, line))
		}
	}

	return out.String()
}

// hintLines wraps prose. A hint line indented with abort.Indent is a command
// to copy: it is colored and never wrapped, since a split command would not
// run.
func hintLines(paint Paint, line string) string {
	// A blank line keeps the advice for different things apart.
	if strings.TrimSpace(line) == "" {
		return "\n"
	}

	command, isCommand := strings.CutPrefix(line, abort.Indent)
	if isCommand {
		return StepIndent + abort.Indent + paint.Code.Render(command) + "\n"
	}

	var out strings.Builder

	for _, wrapped := range splitLines(WrapText(line, hintWidth)) {
		out.WriteString(StepIndent + paint.Text.Render(wrapped) + "\n")
	}

	return out.String()
}

// StyleHelp colors section headings and the first column of command, flag,
// and example rows. A long flag is indented further to line up under the
// short ones. Other deeper lines continue a description and stay plain.
func StyleHelp(text string, paint Paint) string {
	lines := strings.Split(text, "\n")

	for index, line := range lines {
		body := strings.TrimLeft(line, " ")

		switch {
		case helpSection.MatchString(line):
			lines[index] = paint.Title.Render(line)
		case strings.HasPrefix(line, StepIndent) && !strings.HasPrefix(line, StepIndent+" "),
			strings.HasPrefix(line, StepIndent) && strings.HasPrefix(body, "--"):
			lines[index] = styleHelpRow(line, paint)
		}
	}

	return strings.Join(lines, "\n")
}

func styleHelpRow(line string, paint Paint) string {
	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]

	name, rest, found := strings.Cut(body, "  ")
	if !found {
		return indent + paint.Code.Render(body)
	}

	return indent + paint.Code.Render(name) + "  " + rest
}

// DisplayPath shortens the home directory to ~ for messages.
func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}

	rest, found := strings.CutPrefix(path, home+string(os.PathSeparator))
	if !found {
		return path
	}

	return "~/" + filepath.ToSlash(rest)
}

// FormatElapsed shows seconds under a minute, then minutes and seconds.
func FormatElapsed(elapsed time.Duration) string {
	elapsed = elapsed.Round(time.Second)
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds", int(elapsed/time.Second))
	}

	minutes := int(elapsed / time.Minute)
	seconds := int((elapsed % time.Minute) / time.Second)

	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}
