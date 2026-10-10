package runbooks

import (
	"fmt"
	"strings"

	"github.com/lukaszgard/den/internal/config"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// buildForm creates the prompt for a row's parameters. Values are collected
// into a map of pointers that huh writes through as the user types.
func buildForm(specs []config.ParamSpec, width, height int, isDark bool) (*huh.Form, map[string]*string) {
	values := make(map[string]*string, len(specs))
	fields := make([]huh.Field, 0, len(specs))

	for _, p := range specs {
		v := new(string)
		*v = p.Default
		values[p.Name] = v

		title := p.Name
		desc := formText(p.Description, width)
		if desc == "" {
			desc = " "
		}

		switch {
		case len(p.Options) > 0:
			fields = append(fields, huh.NewSelect[string]().
				Title(title).
				Description(desc).
				Options(huh.NewOptions(p.Options...)...).
				Value(v))
		default:
			in := huh.NewInput().
				Title(title).
				Description(desc).
				Value(v)
			if p.Secret {
				// Masked so an approval token does not end up on a shared screen.
				in = in.EchoMode(huh.EchoModePassword)
			}
			if p.Required {
				name := p.Name
				in = in.Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("%s is required", name)
					}
					return nil
				})
			}
			fields = append(fields, in)
		}
	}

	form := huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(formThemeFor(isDark)).
		WithWidth(formWidth(width)).
		WithHeight(formHeight(height, len(specs))).
		// The pane renders its own hint line; huh's would duplicate it.
		WithShowHelp(false)
	return form, values
}

// collect flattens the live form values into the map the runner expects.
func collect(values map[string]*string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func formWidth(width int) int {
	w := min(width-8, 90)
	if w < 30 {
		w = 30
	}
	return w
}

// formText wraps a field's description to the width a form of
// formWidth(width) shows it at. huh counts a group's height in the lines it is
// given, and the viewport it draws through wraps a longer one, then cuts what
// no longer fits: each line a wrap adds pushes the group's last line — the
// input being typed into — out of sight.
func formText(text string, width int) string {
	t := formStyles()
	frame := max(t.Focused.Base.GetHorizontalFrameSize(), t.Blurred.Base.GetHorizontalFrameSize()) +
		t.Focused.Description.GetHorizontalFrameSize()
	return ansi.Wrap(text, max(formWidth(width)-frame, 10), "")
}

func formHeight(height, fields int) int {
	// Roughly four rows per field, plus room for the help line.
	h := fields*4 + 5
	if max := height - 6; h > max {
		h = max
	}
	if h < 8 {
		h = 8
	}
	return h
}

// formStyles are formTheme's styles for the code that measures a form rather
// than draws one: the sizes of its frames do not depend on the background.
func formStyles() *huh.Styles { return formTheme(true) }

// formThemeFor is the theme of a form drawn on a dark or a light background as
// the terminal reported it. A form embedded in the pane never sees the
// terminal's reply itself, so the pane hands it the answer through the theme.
func formThemeFor(isDark bool) huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles { return formTheme(isDark) })
}

// formTheme keeps the prompt in the pane's own colours rather than huh's
// defaults, so it does not look bolted on.
func formTheme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	accent := lipgloss.Color("#5BC8AF")
	dim := lipgloss.Color("#9B9B9B")

	t.Focused.Title = t.Focused.Title.Foreground(accent).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(dim)
	t.Focused.Base = t.Focused.Base.BorderForeground(accent)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(accent)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accent)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(accent)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accent)
	t.Blurred.Title = t.Blurred.Title.Foreground(dim)
	t.Blurred.Description = t.Blurred.Description.Foreground(dim)
	return t
}
