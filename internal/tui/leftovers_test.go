package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lukaszgard/den/internal/service"
	"github.com/lukaszgard/den/internal/tui/testkeys"
)

// fakeLeftoverSvc is a VPN that finds left the processes in left, which a
// stop ends when stopClears is set.
type fakeLeftoverSvc struct {
	*fakeSvc
	left       *service.Leftovers
	stopClears bool
}

func (f *fakeLeftoverSvc) Leftovers(context.Context) *service.Leftovers {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.left
}

func (f *fakeLeftoverSvc) Stop(ctx context.Context) error {
	_ = f.fakeSvc.Stop(ctx)
	f.mu.Lock()
	if f.stopClears {
		f.left = nil
	}
	f.mu.Unlock()
	return nil
}

// leftPPPD is the pppd that outlived the stop of its openfortivpn by days.
func leftPPPD() *service.Leftovers {
	return &service.Leftovers{
		Processes:   []service.Leftover{{Name: "pppd", PID: "29035", Age: 3*24*time.Hour + 13*time.Hour}},
		KillCommand: "sudo kill 29035; sleep 5; sudo kill -9 29035 2>/dev/null",
		StopCommand: "sudo /opt/den/libexec/den-vpn stop office-vpn",
	}
}

func leftoverVPN(name string, left *service.Leftovers) *fakeLeftoverSvc {
	return &fakeLeftoverSvc{fakeSvc: newSvc(service.TypeVPN, name), left: left}
}

// vpnHarness opens the VPN pane, which has the keyboard.
func vpnHarness(t *testing.T, svcs ...service.Service) *harness {
	t.Helper()
	h := newHarness(t, managerWith(svcs...), nil)
	h.openPane(paneVPN)
	h.press("right")
	return h
}

func (h *harness) screenHas(wants ...string) {
	h.t.Helper()
	screen := plain(h.m)
	for _, want := range wants {
		if !strings.Contains(screen, want) {
			h.t.Errorf("the screen does not show %q:\n%s", want, screen)
		}
	}
}

// den used to refuse a connect over a leftover pppd with a log line, and the
// user had to work out the next step from it. A c now names what is left and
// how to stop it, and starts nothing until asked to.
func TestConnectOffersToStopALeftoverVPN(t *testing.T) {
	vpn := leftoverVPN("Office VPN", leftPPPD())
	h := vpnHarness(t, vpn)

	h.press("c")
	if h.m.leftovers == nil {
		t.Fatal("c over a leftover pppd should open the pop-up")
	}
	h.screenHas("Office VPN: a VPN from an earlier session is still running", "pppd  pid 29035", "running 3 days",
		vpn.left.StopCommand, vpn.left.KillCommand, "y: stop it and connect", "s: copy stop", "k: copy kill", "n: cancel")
	if strings.Contains(plain(h.m), "admin password") {
		t.Error("the helper's stop asks for no password")
	}
	if starts, stops, _ := vpn.counts(); starts != 0 || stops != 0 {
		t.Fatalf("starts=%d stops=%d before an answer, want 0 and 0", starts, stops)
	}

	h.press("n")
	if h.m.leftovers != nil {
		t.Fatal("n should close the pop-up")
	}
	if starts, stops, _ := vpn.counts(); starts != 0 || stops != 0 {
		t.Fatalf("starts=%d stops=%d after n, want 0 and 0", starts, stops)
	}
	if want := "not connecting Office VPN: pppd (pid 29035) is still running"; h.m.flashMsg != want || !h.m.flashWarn {
		t.Errorf("flash = %q (warn %v), want the warning %q", h.m.flashMsg, h.m.flashWarn, want)
	}
}

func TestYesStopsTheLeftoverThenConnects(t *testing.T) {
	vpn := leftoverVPN("Office VPN", leftPPPD())
	vpn.stopClears = true
	h := vpnHarness(t, vpn)

	h.press("c", "y")
	if starts, stops, _ := vpn.counts(); starts != 1 || stops != 1 {
		t.Fatalf("starts=%d stops=%d, want one stop, then one start", starts, stops)
	}
	if h.m.leftovers != nil || h.m.flashMsg != "connecting..." {
		t.Fatalf("pop-up %v, flash %q; want it closed and connecting", h.m.leftovers != nil, h.m.flashMsg)
	}
}

// A stop that leaves the process running must not be followed by a connect
// beside it: the pop-up says what is left, and offers to try again.
func TestYesThatLeavesItRunningSaysSo(t *testing.T) {
	vpn := leftoverVPN("Office VPN", leftPPPD())
	h := vpnHarness(t, vpn)

	h.press("c", "y")
	if starts, stops, _ := vpn.counts(); starts != 0 || stops != 1 {
		t.Fatalf("starts=%d stops=%d, want one stop and no start", starts, stops)
	}
	if h.m.leftovers == nil || !h.m.leftovers.stopFailed {
		t.Fatal("the pop-up should open again, saying den's stop left it")
	}
	h.screenHas("Office VPN: den's stop left this running", "y: try again", "esc: close", vpn.left.KillCommand)

	vpn.mu.Lock()
	vpn.stopClears = true
	vpn.mu.Unlock()
	h.press("y")
	if starts, stops, _ := vpn.counts(); starts != 1 || stops != 2 {
		t.Fatalf("after trying again: starts=%d stops=%d, want 1 and 2", starts, stops)
	}
}

func TestDisconnectThatLeavesAProcessSaysSo(t *testing.T) {
	vpn := leftoverVPN("Office VPN", leftPPPD())
	vpn.status = service.StatusError
	h := vpnHarness(t, vpn)

	h.press("d")
	if _, stops, _ := vpn.counts(); stops != 1 {
		t.Fatalf("stops = %d, want 1", stops)
	}
	if h.m.leftovers == nil || !h.m.leftovers.stopFailed || h.m.leftovers.connect {
		t.Fatal("a d that left pppd running should open the pop-up, without connecting after")
	}
	h.screenHas("den's stop left this running", "y: try again")
	h.press("esc")
	if h.m.leftovers != nil || strings.Contains(h.m.flashMsg, "not connecting") {
		t.Fatalf("esc: pop-up %v, flash %q; nothing was connecting", h.m.leftovers != nil, h.m.flashMsg)
	}

	// A d that ends everything opens nothing.
	vpn.mu.Lock()
	vpn.stopClears = true
	vpn.mu.Unlock()
	h.press("d")
	if h.m.leftovers != nil || h.m.flashMsg != "stopped" {
		t.Fatalf("a clean d: pop-up %v, flash %q", h.m.leftovers != nil, h.m.flashMsg)
	}
	if starts, _, _ := vpn.counts(); starts != 0 {
		t.Fatalf("a d started the VPN %d times", starts)
	}
}

func TestLeftoverCommandsAreCopied(t *testing.T) {
	copied := clipboard(t)
	vpn := leftoverVPN("Office VPN", leftPPPD())
	h := vpnHarness(t, vpn)

	h.press("c", "k")
	if *copied != vpn.left.KillCommand || h.m.leftovers == nil {
		t.Fatalf("k copied %q (pop-up open %v), want the kill command, the pop-up still open", *copied, h.m.leftovers != nil)
	}
	h.press("s")
	if *copied != vpn.left.StopCommand || h.m.leftovers == nil {
		t.Fatalf("s copied %q, want the helper's stop", *copied)
	}

	// Without the helper there is no stop command to offer, and the stop
	// raises the admin dialog.
	l := leftPPPD()
	l.StopCommand, l.AdminPrompt = "", true
	*copied = ""
	h = vpnHarness(t, leftoverVPN("Office VPN", l))
	h.press("c")
	if strings.Contains(plain(h.m), "copy stop") || strings.Contains(plain(h.m), "den-vpn stop") {
		t.Error("the helper's stop is offered without the helper")
	}
	h.screenHas("k: copy kill", "Stopping it asks for your admin password.")
	h.press("s")
	if *copied != "" {
		t.Errorf("s copied %q without a stop command", *copied)
	}
}

func TestConnectWithNothingLeftStartsAsBefore(t *testing.T) {
	vpn := leftoverVPN("Office VPN", nil)
	h := vpnHarness(t, vpn)

	h.press("c")
	if starts, _, _ := vpn.counts(); starts != 1 || h.m.leftovers != nil || h.m.flashMsg != "connecting..." {
		t.Fatalf("starts=%d pop-up=%v flash=%q", starts, h.m.leftovers != nil, h.m.flashMsg)
	}
}

// With another of den's VPNs up, its live openfortivpn is what a check would
// find, and the pop-up would offer to kill a working tunnel.
func TestConnectBesideAnotherVPNWarns(t *testing.T) {
	office := leftoverVPN("Office VPN", leftPPPD())
	lab := leftoverVPN("Lab VPN", nil)
	lab.status = service.StatusConnected
	h := vpnHarness(t, office, lab)
	if svc, ok := h.m.vpnPane.SelectedService(); !ok || svc.Info().Name != "Office VPN" {
		t.Fatal("test setup: Office VPN should be selected")
	}

	h.press("c")
	if h.m.leftovers != nil {
		t.Fatal("c opened the pop-up over another VPN's tunnel")
	}
	if starts, _, _ := office.counts(); starts != 0 {
		t.Fatalf("starts = %d, want 0", starts)
	}
	if want := "Lab VPN is connected — den runs one VPN at a time; disconnect it first"; h.m.flashMsg != want {
		t.Errorf("flash = %q, want %q", h.m.flashMsg, want)
	}

	h.press("d")
	if _, stops, _ := office.counts(); stops != 1 || h.m.leftovers != nil {
		t.Fatalf("d: stops=%d pop-up=%v; want a plain stop", stops, h.m.leftovers != nil)
	}
}

// The pop-up takes every key, and nothing else answers it: a d meant for the
// pane, a stray click, a drag or a paste must not stop or start anything, nor
// copy what the pop-up covers.
func TestTheLeftoverPopUpKeepsKeysAndClicks(t *testing.T) {
	copied := clipboard(t)
	vpn := leftoverVPN("Office VPN", leftPPPD())
	h := vpnHarness(t, vpn)

	h.press("c", "d")
	h.click(70, 20)
	h.dragOver(45, 20, 100, 22)
	h.send(testkeys.Paste("y"))
	if starts, stops, _ := vpn.counts(); starts != 0 || stops != 0 || h.m.leftovers == nil {
		t.Fatalf("starts=%d stops=%d pop-up=%v; the pop-up should still be waiting", starts, stops, h.m.leftovers != nil)
	}
	if *copied != "" {
		t.Errorf("a drag over the pop-up copied %q", *copied)
	}
}

func TestRunningFor(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12 * time.Second:               "12 s",
		4*time.Minute + 10*time.Second: "4 min",
		13*time.Hour + 5*time.Minute:   "13 h",
		47 * time.Hour:                 "47 h",
		3*24*time.Hour + 13*time.Hour:  "3 days",
	} {
		if got := runningFor(d); got != want {
			t.Errorf("runningFor(%v) = %q, want %q", d, got, want)
		}
	}
}
