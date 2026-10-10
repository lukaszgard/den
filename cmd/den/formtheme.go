package main

import (
	"os"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// standaloneTheme is the theme for a form that runs on its own, outside the
// TUI. Such a form never asks the terminal for its background, so it would
// draw the light palette on any terminal; asking here is safe because no
// Bubble Tea program owns stdin yet, which is why the TUI does not do it.
func standaloneTheme() huh.Theme {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	return huh.ThemeFunc(func(bool) *huh.Styles { return huh.ThemeCharm(isDark) })
}
