// Package termsession runs an interactive program on a pseudo-terminal and
// keeps the screen it draws, so den can show a database client inside one of
// its own views instead of stepping aside for it.
//
// A Session is the program, its pty (a Unix pty, ConPTY on Windows) and a
// terminal emulator fed with everything the program writes. Nothing here knows
// about the TUI: a view asks for View, sends keys, and waits on Changed.
package termsession

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// Command is the program a session runs.
type Command struct {
	Path string
	Args []string // without the program name
	// Env is added to den's own environment. A credential belongs here
	// rather than in Args, which any user can read from the process list.
	Env []string
	// Dir is the directory the program starts in; den's own when empty.
	Dir string
}

const (
	// termName is what the client is told it draws on: the emulator, not the
	// terminal den itself runs in.
	termName = "xterm-256color"
	// drainWait is how long an exited program's last output gets to arrive
	// before its pty is closed. It bounds the wait for a pty that a stray
	// grandchild still holds open and that would otherwise never end.
	drainWait = 300 * time.Millisecond
	// killAfter is how long a closed session's programs get to leave on a
	// hangup before they are killed.
	killAfter = 2 * time.Second
)

// Session is one running program and its screen.
type Session struct {
	pty    xpty.Pty
	cmd    *exec.Cmd
	cancel context.CancelFunc

	// mu guards the emulator, which is fed by the pty reader and read by the
	// view, and the fields below it.
	mu           sync.Mutex
	emu          *vt.Emulator
	cols, rows   int
	cursorHidden bool
	// cursorStyle and cursorSteady are what the program asked for with DECSCUSR:
	// vim switches to a bar for insert mode.
	cursorStyle  vt.CursorStyle
	cursorSteady bool
	// altScreen and appCursorKeys are what the program switched on: see FullScreen.
	altScreen, appCursorKeys bool
	exitErr                  error

	out     *outbox
	changed chan struct{}
	done    chan struct{}

	closeOnce sync.Once
}

// Start runs cmd on a new pty of cols×rows.
func Start(cmd Command, cols, rows int) (*Session, error) {
	cols, rows = max(cols, 1), max(rows, 1)
	p, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("open a terminal: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := exec.Command(cmd.Path, cmd.Args...)
	c.Env = append(append(environ(), "TERM="+termName), cmd.Env...)
	c.Dir = cmd.Dir
	c.SysProcAttr = sessionAttr()
	if err := p.Start(c); err != nil {
		cancel()
		_ = p.Close()
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	// The parent's copy of the pty's far end has to go, or reading the pty
	// would never see the program leave.
	if u, ok := p.(*xpty.UnixPty); ok {
		_ = u.Slave().Close()
	}

	s := &Session{
		pty:     p,
		cmd:     c,
		cancel:  cancel,
		emu:     vt.NewEmulator(cols, rows),
		cols:    cols,
		rows:    rows,
		out:     newOutbox(),
		changed: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	// The callbacks run inside emu.Write, which already holds mu.
	s.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.cursorHidden = !visible },
		// The emulator names the second argument blink but passes whether the
		// cursor is steady (TestCursorFollowsWhatTheProgramAsksFor pins it).
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			s.cursorStyle, s.cursorSteady = style, steady
		},
		AltScreen: func(on bool) { s.altScreen = on },
		EnableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeCursorKeys {
				s.appCursorKeys = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeCursorKeys {
				s.appCursorKeys = false
			}
		},
	})

	readDone := make(chan struct{})
	go s.read(readDone)
	// The emulator hands over keys, pastes and its answers to the program's
	// terminal queries through a pipe that blocks until it is read. The outbox
	// takes them at once, so a program that is not reading its input can
	// stall neither the emulator nor the caller of SendKey.
	go func() { _, _ = io.Copy(s.out, s.emu) }()
	go s.out.drain(p)
	go s.wait(ctx, readDone)
	return s, nil
}

// environ is den's environment without LINES and COLUMNS. Those describe the
// terminal den itself runs in, and a pager that trusts them over the pty's
// size would draw for the wrong one.
func environ() []string {
	env := os.Environ()
	kept := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "LINES=") && !strings.HasPrefix(kv, "COLUMNS=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

// read feeds the program's output to the emulator until the pty ends.
func (s *Session) read(done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.emu.Write(buf[:n])
			s.mu.Unlock()
			s.notify()
		}
		if err != nil {
			return
		}
	}
}

// wait ends the session once the program has exited.
func (s *Session) wait(ctx context.Context, readDone <-chan struct{}) {
	err := xpty.WaitProcess(ctx, s.cmd)
	select {
	case <-readDone:
	case <-time.After(drainWait):
	}
	_ = s.pty.Close()
	s.out.close()

	s.mu.Lock()
	// Closing the emulator itself would write a flag its Read checks without
	// a lock; closing the pipe's writing end stops that Read just the same.
	if c, ok := s.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
	// A cancelled context only ever means Close: that is not a failure of
	// the program worth reporting.
	if ctx.Err() == nil {
		s.exitErr = err
	}
	s.mu.Unlock()
	close(s.done)
	s.notify()
}

func (s *Session) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Changed receives when the screen may have changed or the program has
// exited. Signals are coalesced: one may stand for many changes.
func (s *Session) Changed() <-chan struct{} { return s.changed }

// Done is closed once the program has exited and its last output is on screen.
func (s *Session) Done() <-chan struct{} { return s.done }

// Alive reports whether the program is still running.
func (s *Session) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// ExitErr is how the program ended: nil after a clean exit or a Close, an
// *exec.ExitError otherwise. It is nil while the program runs.
func (s *Session) ExitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// FullScreen reports that a program which handles every key itself has the
// terminal: an editor or a pager, rather than a prompt reading a line. Such
// programs switch to the alternate screen, or at least, when told not to
// (less -X), put the arrows into application mode; both are undone when they
// leave. An exited program is never full-screen, whatever it left switched on.
//
// The arrows alone do not count while the program den started is itself in
// front on the terminal: zsh with oh-my-zsh switches them at every prompt
// (echoti smkx), and a shell reading a line is no editor. A pager started from
// that shell is a job of its own, so it still counts.
func (s *Session) FullScreen() bool {
	s.mu.Lock()
	alt, arrows := s.altScreen, s.appCursorKeys
	s.mu.Unlock()
	if !s.Alive() {
		return false
	}
	return alt || (arrows && !s.programInFront())
}

// programInFront reports whether the program den started, rather than a job
// it runs, is the terminal's foreground process group. It leads its session,
// so its group's id is its pid. False where that cannot be told (Windows).
func (s *Session) programInFront() bool {
	if s.cmd.Process == nil {
		return false
	}
	pgrp, ok := foreground(s.pty)
	return ok && pgrp == s.cmd.Process.Pid
}

// SendKey sends a key press, encoded for the mode the program put the
// terminal in (arrow keys differ inside a pager).
func (s *Session) SendKey(k vt.KeyPressEvent) {
	s.input(func() { s.emu.SendKey(k) })
}

// SendText sends typed text.
func (s *Session) SendText(text string) {
	s.input(func() { s.emu.SendText(text) })
}

// Paste sends pasted text, bracketed when the program asked for that, so a
// pasted newline is not mistaken for enter.
func (s *Session) Paste(text string) {
	s.input(func() { s.emu.Paste(text) })
}

// input runs send unless the program is gone: nothing reads its terminal
// then, and the emulator's pipe would hold the caller for ever.
func (s *Session) input(send func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Alive() {
		send()
	}
}

// Resize changes the terminal's size. The screen of an exited program is
// resized too, so it still fits the view that shows it.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	s.mu.Lock()
	if cols == s.cols && rows == s.rows {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	s.emu.Resize(cols, rows)
	alive := s.Alive()
	s.mu.Unlock()
	if alive {
		_ = s.pty.Resize(cols, rows)
	}
}

// View renders the screen: one line per row, styles as ANSI sequences. The
// cursor is not in it: Cursor says where the terminal's own goes.
func (s *Session) View() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Render()
}

// Cursor is the program's cursor: where it stands on the screen (column and
// row from the top left) and how it asked to have it drawn.
type Cursor struct {
	X, Y  int
	Style vt.CursorStyle
	Blink bool
}

// Cursor reports the cursor to show, or false while the program hides it, has
// exited, or has it outside the screen.
func (s *Session) Cursor() (Cursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursorHidden || !s.Alive() {
		return Cursor{}, false
	}
	pos := s.emu.CursorPosition()
	if pos.X < 0 || pos.Y < 0 || pos.X >= s.cols || pos.Y >= s.rows {
		return Cursor{}, false
	}
	return Cursor{X: pos.X, Y: pos.Y, Style: s.cursorStyle, Blink: !s.cursorSteady}, true
}

// Close stops the program and everything it started. It returns at once;
// Done is closed when they are gone.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		if !s.Alive() {
			return
		}
		s.cancel()
		hangUp(s.cmd)
		go func() {
			select {
			case <-s.done:
			case <-time.After(killAfter):
				kill(s.cmd)
			}
		}()
	})
}

// outbox is an unbounded-enough queue of bytes on their way to the program.
type outbox struct {
	mu     sync.Mutex
	more   *sync.Cond
	buf    []byte
	closed bool
}

// outboxLimit is where the outbox starts dropping: far more than anyone
// types or pastes ahead of a program that has stopped reading.
const outboxLimit = 1 << 20

func newOutbox() *outbox {
	o := &outbox{}
	o.more = sync.NewCond(&o.mu)
	return o
}

// Write queues p and never blocks.
func (o *outbox) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.closed && len(o.buf)+len(p) <= outboxLimit {
		o.buf = append(o.buf, p...)
		o.more.Signal()
	}
	return len(p), nil
}

// drain writes what is queued to w until the outbox is closed or w fails.
func (o *outbox) drain(w io.Writer) {
	for {
		o.mu.Lock()
		for len(o.buf) == 0 && !o.closed {
			o.more.Wait()
		}
		if o.closed {
			o.mu.Unlock()
			return
		}
		chunk := o.buf
		o.buf = nil
		o.mu.Unlock()
		if _, err := w.Write(chunk); err != nil {
			o.close()
			return
		}
	}
}

func (o *outbox) close() {
	o.mu.Lock()
	o.closed = true
	o.buf = nil
	o.mu.Unlock()
	o.more.Signal()
}
