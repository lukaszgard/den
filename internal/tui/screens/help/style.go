package help

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Every section of a topic and every group of the list has an icon and a
// colour of its own, so a reader sees where they are at a glance. Nerd Font
// glyphs are monochrome: den colours them. Each is in the Basic Multilingual
// Plane's private use area and in Nerd Fonts since v2, so it is one cell wide
// in any Nerd Font a user has.

// look is an icon and the colour it and its text are drawn in.
type look struct {
	icon  string
	color adaptive
}

// adaptive is a colour for each kind of background.
type adaptive struct{ Dark, Light string }

func colour(dark, light string) adaptive {
	return adaptive{Dark: dark, Light: light}
}

// resolve is the colour for a dark or a light background, as the terminal
// reported it.
func (a adaptive) resolve(isDark bool) color.Color {
	return lipgloss.LightDark(isDark)(lipgloss.Color(a.Light), lipgloss.Color(a.Dark))
}

// templateSection is one ## section a topic may have. Topics hold them in
// this order; topics_test.go checks every topic against the list.
type templateSection struct {
	title    string
	required bool
	look
}

var templateSections = []templateSection{
	{"What it is", true, look{"", colour("#B794F4", "#6B46C1")}},          // info-circle
	{"What you can do", true, look{"", colour("#68D391", "#2F855A")}},     // bolt
	{"Before you start", true, look{"", colour("#F6AD55", "#B7791F")}},    // tasks
	{"Configure", false, look{"", colour("#63B3ED", "#2B6CB0")}},          // cog: left out when den.yaml has nothing
	{"Usage", false, look{"", colour("#4FD1C5", "#2C7A7B")}},              // play: step by step, where it helps
	{"Limitations", true, look{"", colour("#ED8936", "#C05621")}},         // ban
	{"When it goes wrong", false, look{"", colour("#FC8181", "#C53030")}}, // wrench: where there is something to say
	{"See also", true, look{"", colour("#A0AEC0", "#4A5568")}},            // link
}

// groupLooks are the list's groups, by their title in the index. The dark
// colours are the menu's own where a panel fits the group.
var groupLooks = map[string]look{
	"Getting started": {"", colour("#B794F4", "#6B46C1")}, // rocket
	"Connecting":      {"", colour("#A78BFA", "#5B21B6")}, // exchange
	"Data stores":     {"", colour("#F4A261", "#B45309")}, // database
	"Secrets":         {"", colour("#E9C46A", "#A16207")}, // lock
	"Automation":      {"", colour("#5BC8AF", "#0F766E")}, // terminal
	"den itself":      {"", colour("#4FD1C5", "#2C7A7B")}, // sliders
}

// helpLook is the Help entry's own, for a group without one.
var helpLook = look{"", colour("#B794F4", "#6B46C1")}

func groupLook(group string) look {
	if l, ok := groupLooks[group]; ok {
		return l
	}
	return helpLook
}

func rule(isDark bool) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colour("#4A4A4A", "#CBD5E0").resolve(isDark))
}

// headingLine restyles a heading glamour rendered as "## What it is" into the
// section's icon and name in its colour, and a rule to the end of the line.
// Any other line is left alone.
func headingLine(plain string, width int, isDark bool) (string, bool) {
	text := strings.TrimLeft(plain, " ")
	name, ok := strings.CutPrefix(text, "## ")
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(name)
	for _, s := range templateSections {
		if s.title != name {
			continue
		}
		indent := plain[:len(plain)-len(text)]
		line := indent + lipgloss.NewStyle().Bold(true).Foreground(s.color.resolve(isDark)).Render(s.icon+"  "+name)
		if n := width - lipgloss.Width(line) - 1; n > 0 {
			line += " " + rule(isDark).Render(strings.Repeat("─", n))
		}
		return line, true
	}
	return "", false
}

// header is what an open topic shows above its box: the group's icon and the
// title in the group's colour, then the summary, cut to width.
func header(t Topic, l look, width int, isDark bool) string {
	line := lipgloss.NewStyle().Bold(true).Foreground(l.color.resolve(isDark)).Render(l.icon + " " + t.Title)
	if room := width - lipgloss.Width(line) - 3; room >= 12 {
		line += "   " + dim.Render(ansi.Truncate(t.Summary, room, "…"))
	}
	return line
}
