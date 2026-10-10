package sigv4proxy

import (
	"reflect"
	"testing"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/service/ssmfwd"
)

// There is no client to log in behind the proxy: the terminal is the user's
// shell, told where the proxy listens. It carries no credential, because the
// proxy signs.
func TestTerminalIsAShellThatKnowsTheProxy(t *testing.T) {
	e := NewEngine(EngineOptions{Type: service.TypeOpenSearch, Host: "vpc-x.es.amazonaws.com", LocalPort: 59200})
	tc, ok := e.(ssmfwd.TerminalCommander)
	if !ok {
		t.Fatal("the proxy engine offers no terminal command")
	}

	got := tc.TerminalCommand("")
	want := service.ClientCommand{Tool: service.ShellTool, Env: []string{"DEN_URL=http://localhost:59200"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalCommand =\n%+v\nwant\n%+v", got, want)
	}
}
