package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/msgs"
)

// A VPN cannot connect while a hung openfortivpn, or the pppd one left behind,
// still holds ppp0 and the DNS, and den refuses to start one beside it. Before
// this pop-up the user had to work out the next step from a log line. Now a c
// lists what is left and offers to stop it, or shows the commands to stop it by
// hand; so does a d whose stop left something running. It never opens by
// itself: without the helper, den's stop raises an admin dialog.

// leftoversMsg reports what keeps svc from connecting, found off the UI
// goroutine (ps, sudo -n).
type leftoversMsg struct {
	svc       service.Service
	l         *service.Leftovers // nil when nothing is left
	connect   bool               // a c asked: connect once nothing is left
	afterStop bool               // den's stop ran first, so l is what it left
}

// leftoverPrompt is the pop-up listing svc's leftovers.
type leftoverPrompt struct {
	svc        service.Service
	l          *service.Leftovers
	connect    bool // y connects once they are gone
	stopFailed bool // den's stop already ran and left them
}

// checkLeftovers reports svc's leftovers after a c.
func checkLeftovers(ctx context.Context, svc service.Service, la service.LeftoverAware) tea.Cmd {
	return func() tea.Msg { return leftoversMsg{svc: svc, l: la.Leftovers(ctx), connect: true} }
}

// stopThenCheck runs den's stop on svc, then reports what it left.
func stopThenCheck(ctx context.Context, svc service.Service, la service.LeftoverAware, connect bool) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Stop(ctx); err != nil {
			return msgs.ErrorMsg{Err: err}
		}
		return leftoversMsg{svc: svc, l: la.Leftovers(ctx), connect: connect, afterStop: true}
	}
}

// otherRunning names another service of svc's type that is up or on its way
// up, "" when there is none. svc's leftovers would then be that one's live
// processes, and the pop-up would offer to kill them.
func (m *AppModel) otherRunning(svc service.Service) string {
	info := svc.Info()
	for _, s := range m.manager.Services() {
		if other := s.Info(); other.Name != info.Name && other.Type == info.Type && isRunning(s) {
			return fmt.Sprintf("%s is %s", other.Name, s.State().Status)
		}
	}
	return ""
}

// leftoversFound opens the pop-up when something is left, and otherwise does
// what was asked.
func (m *AppModel) leftoversFound(msg leftoversMsg) []tea.Cmd {
	if msg.l != nil && len(msg.l.Processes) > 0 {
		m.leftovers = &leftoverPrompt{svc: msg.svc, l: msg.l, connect: msg.connect, stopFailed: msg.afterStop}
		return nil
	}
	switch {
	case msg.connect:
		return m.startCmds(msg.svc)
	case msg.afterStop:
		return []tea.Cmd{func() tea.Msg { return msgs.StatusFlashMsg{Text: "stopped"} }}
	}
	return nil
}

// leftoverPromptKey handles a key while the pop-up is open; like the reload
// pop-up it takes every key, so a d pressed while reading it never reaches the
// pane behind.
func (m *AppModel) leftoverPromptKey(k string) tea.Cmd {
	p := m.leftovers
	switch k {
	case "y":
		m.leftovers = nil
		la, _ := p.svc.(service.LeftoverAware)
		return tea.Batch(
			stopThenCheck(m.ctx, p.svc, la, p.connect),
			func() tea.Msg { return msgs.StatusFlashMsg{Text: "stopping…"} },
		)
	case "s":
		if p.l.StopCommand != "" {
			return components.Copy(p.l.StopCommand, "copied!")
		}
	case "k":
		return components.Copy(p.l.KillCommand, "copied!")
	case "n", "esc", "q":
		m.leftovers = nil
		if p.connect {
			text := fmt.Sprintf("not connecting %s: %s", p.svc.Info().Name, p.running())
			return func() tea.Msg { return warnFlash(text) }
		}
	}
	return nil
}

// running says what is still running, briefly.
func (p *leftoverPrompt) running() string {
	names := make([]string, len(p.l.Processes))
	for i, pr := range p.l.Processes {
		names[i] = fmt.Sprintf("%s (pid %s)", pr.Name, pr.PID)
	}
	if len(names) == 1 {
		return names[0] + " is still running"
	}
	return strings.Join(names, ", ") + " are still running"
}

// view draws the pop-up for a screen of width×height. The commands wrap rather
// than being cut, since the whole command is the point of showing it.
func (p *leftoverPrompt) view(width int) string {
	inner := max(min(width-6, promptWidth), 10)
	title := p.svc.Info().Name + ": a VPN from an earlier session is still running"
	note := "A new connect cannot run beside it: it holds ppp0 and the VPN's DNS."
	if p.stopFailed {
		these := "these"
		if len(p.l.Processes) == 1 {
			these = "this"
		}
		title = p.svc.Info().Name + ": den's stop left " + these + " running"
		note = "They hold ppp0 and the VPN's DNS, so no new connect can come up."
	}
	content := []string{
		ansi.Truncate(promptTitle.Render(title), inner, "…"),
		ansi.Truncate(promptNote.Render(note), inner, "…"),
		"",
	}

	nameW := 0
	for _, pr := range p.l.Processes {
		nameW = max(nameW, len(pr.Name))
	}
	for _, pr := range p.l.Processes {
		row := fmt.Sprintf("  %-*s  pid %-7s", nameW, pr.Name, pr.PID)
		if pr.Age > 0 {
			row += "  running " + runningFor(pr.Age)
		}
		content = append(content, ansi.Truncate(strings.TrimRight(row, " "), inner, "…"))
	}

	content = append(content, "", promptNote.Render("To stop it yourself:"))
	for _, c := range []string{p.l.StopCommand, p.l.KillCommand} {
		if c == "" {
			continue
		}
		for i, line := range strings.Split(ansi.Wrap(c, inner-4, ""), "\n") {
			indent := "  "
			if i > 0 {
				indent = "    " // the same command, carried on
			}
			content = append(content, indent+line)
		}
	}
	if p.l.AdminPrompt {
		content = append(content, "", ansi.Truncate(promptWarn.Render("Stopping it asks for your admin password."), inner, "…"))
	}

	// Without stopFailed the pop-up came from a c: den's stop has not run yet.
	footer := []components.HelpKey{{Key: "y", Text: "stop it and connect"}}
	if p.stopFailed {
		footer[0].Text = "try again"
	}
	if p.l.StopCommand != "" {
		footer = append(footer, components.HelpKey{Key: "s", Text: "copy stop"})
	}
	footer = append(footer, components.HelpKey{Key: "k", Text: "copy kill"})
	if p.stopFailed {
		footer = append(footer, components.HelpKey{Key: "esc", Text: "close"})
	} else {
		footer = append(footer, components.HelpKey{Key: "n", Text: "cancel"})
	}
	content = append(content, "", ansi.Truncate(components.Hints(footer...), inner, "…"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6C91BF")).
		Padding(0, 2).
		Render(strings.Join(content, "\n"))
}

// runningFor says how long a process has run, briefly: "3 days", "13 h",
// "4 min".
func runningFor(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}
