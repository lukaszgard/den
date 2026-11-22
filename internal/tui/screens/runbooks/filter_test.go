package runbooks

import (
	"context"
	"strings"
	"testing"

	"github.com/lukaszgard/den/internal/config"
	"github.com/lukaszgard/den/internal/runbook"
	"github.com/lukaszgard/den/internal/tui/components"
	"github.com/lukaszgard/den/internal/tui/testkeys"

	"github.com/charmbracelet/x/ansi"
)

// The filter lights up what it matched in the two columns it reads, the name
// and the description, as a match looks everywhere else.
func TestFilterLightsUpNameAndDescription(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg, err := runbook.NewRegistry(&config.Config{Runbooks: &config.RunbooksConfig{Items: []config.RunbookSpec{
		{Name: "deploy-api", Description: "Roll out the API", Command: "true"},
		{Name: "rotate-keys", Description: "New signing keys", Command: "true"},
	}}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, context.Background(), func(string) int { return 0 }, 140, 30)
	m, _ = m.Update(testkeys.Press("/"), true)
	m, _ = m.Update(testkeys.Press("api"), true)
	v := m.View(140, 30, true)
	if strings.Contains(ansi.Strip(v), "rotate-keys") {
		t.Fatalf("the filter should hide rotate-keys:\n%s", ansi.Strip(v))
	}
	for _, want := range []string{"api", "API"} {
		if !strings.Contains(v, components.MatchStyle.Render(want)) {
			t.Errorf("%q should light up:\n%q", want, v)
		}
	}
}
