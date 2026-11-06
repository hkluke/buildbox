package wizard

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A first run is guided: the sections follow one another from the welcome
// to the overview, which is then the review, rather than each returning to
// the overview. Esc walks back the same way. Once the review opens, the
// sections return to it as they do on any later run.

const (
	railDone = "●"
	railTodo = "○"
	railGap  = " · "
)

// guidedSections is the order a first run meets the sections in.
func guidedSections() []section {
	return []section{sectionBox, sectionAgent, sectionTailnet}
}

// nextGuidedSection is the section after sec on a first run. A tailnet set
// up in an earlier run is skipped: there is nothing left to do in it.
func (wiz Model) nextGuidedSection(sec section) (section, bool) {
	order := guidedSections()

	for index := slices.Index(order, sec) + 1; index < len(order); index++ {
		if order[index] == sectionTailnet && wiz.tailnetSetUp() {
			continue
		}

		return order[index], true
	}

	return sectionBox, false
}

// begin leaves the welcome for the first section. What the catalog settled
// counts as saved from here, so quitting asks only about later changes.
func (wiz Model) begin() (Model, tea.Cmd) {
	wiz.begun = true
	wiz.saved = wiz.Config()

	return wiz.openSection(sectionBox)
}

// guidedBack is esc on a section's first step during a first run: the last
// step of the section before, or the welcome from the first section.
func (wiz Model) guidedBack(sec section) (Model, tea.Cmd) {
	order := guidedSections()

	index := slices.Index(order, sec)
	if index <= 0 {
		wiz.step = stepLoad
		wiz.note = ""
		wiz.input.Blur()

		return wiz, nil
	}

	steps := wiz.sectionSteps(order[index-1])
	wiz.step = steps[len(steps)-1]
	wiz.note = ""

	return wiz.prepareStep()
}

// journey is a first run's place in the whole, for the header: every
// section with a dot per step, the ones done filled, the current one in the
// accent, the rest hollow. A tailnet with nothing left to do shows a check.
func (wiz Model) journey() string {
	current, ok := sectionOf(wiz.step)
	if !ok || !wiz.guided {
		return ""
	}

	parts := make([]string, 0, len(guidedSections()))

	for _, sec := range guidedSections() {
		name := wiz.paint.Faint.Render(sectionName(sec))
		if sec == current {
			name = wiz.paint.Text.Render(sectionName(sec))
		}

		parts = append(parts, name+" "+wiz.railDots(sec, current))
	}

	return strings.Join(parts, wiz.paint.Faint.Render(railGap))
}

func (wiz Model) railDots(sec, current section) string {
	if sec == sectionTailnet && sec != current && wiz.tailnetSetUp() {
		return wiz.paint.OK.Render(okMark)
	}

	steps := wiz.sectionSteps(sec)
	position := slices.Index(steps, wiz.step)

	var dots strings.Builder

	for index := range steps {
		done := sec < current || (sec == current && index < position)
		dots.WriteString(wiz.railDot(done, sec == current && index == position))
	}

	return dots.String()
}

func (wiz Model) railDot(done, here bool) string {
	switch {
	case done:
		return wiz.paint.OK.Render(railDone)
	case here:
		return wiz.paint.Accent.Render(railDone)
	default:
		return wiz.paint.Faint.Render(railTodo)
	}
}
