package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/lukaszgard/den/internal/config"
)

// checkReport is `den config --check --json`.
type checkReport struct {
	Path     string         `json:"path"`
	Errors   int            `json:"errors"`
	Warnings int            `json:"warnings"`
	Problems []checkProblem `json:"problems"`
}

type checkProblem struct {
	Severity string `json:"severity"` // error | warning
	Line     int    `json:"line"`     // 0 when no one line is to blame
	Key      string `json:"key"`      // the YAML path, e.g. services[2].local_port
	Code     string `json:"code"`     // stable, e.g. port-shared
	Message  string `json:"message"`
	Hint     string `json:"hint"` // how to fix it
}

// runConfigCheck prints what is wrong with the den.yaml at path. It fails
// with exit code 1 on an error, and on a warning too when strict.
func runConfigCheck(w io.Writer, path string, asJSON, strict bool) error {
	_, problems, err := config.LoadChecked(path)
	if err != nil {
		return err
	}
	shown := displayPath(path)
	if asJSON {
		report := checkReport{Path: shown, Errors: problems.Errors(), Warnings: problems.Warnings(), Problems: []checkProblem{}}
		for _, p := range problems {
			report.Problems = append(report.Problems, checkProblem{
				Severity: p.Severity.String(), Line: p.Line, Key: p.Key, Code: p.Code, Message: p.Msg, Hint: p.Hint,
			})
		}
		if err := writeJSON(w, report); err != nil {
			return err
		}
	} else {
		printProblems(w, shown, problems, true)
	}
	if problems.Errors() > 0 || (strict && problems.Warnings() > 0) {
		return exitCodeError{code: 1}
	}
	return nil
}

// printProblems lists problems one to a line, path:line first as editors and
// terminals jump to it, each hint under its problem. header adds the count
// above them, or says there are none.
func printProblems(w io.Writer, path string, ps config.Problems, header bool) {
	if len(ps) == 0 {
		if header {
			fmt.Fprintf(w, "%s: no problems\n", path)
		}
		return
	}
	w = colourWriter(w)
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	amber := lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#9B9B9B"))
	if header {
		fmt.Fprintf(w, "%s: %s\n", path, ps.Summary())
	}
	for _, p := range ps {
		mark := amber.Render("!")
		if p.Severity == config.SeverityError {
			mark = red.Render("✗")
		}
		where := path
		if p.Line > 0 {
			where = fmt.Sprintf("%s:%d", path, p.Line)
		}
		fmt.Fprintf(w, "%s %s  %s\n", mark, where, p.Msg)
		if p.Hint != "" {
			fmt.Fprintf(w, "    %s\n", dim.Render("→ "+p.Hint))
		}
	}
}

// displayPath is path as someone in this folder would write it: relative when
// it is inside, so that path:line reads like the editor's own.
func displayPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}
