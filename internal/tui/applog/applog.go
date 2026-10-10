package applog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaszgard/den/internal/process"
)

const maxLines = 1000

// Log level constants.
const (
	LevelTrace = -1
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

// Logger is a thread-safe append-only log collector for app-level events.
// Implements io.Writer so it can be used anywhere that accepts a writer.
//
// It also keeps the output of the runs started in this den, apart from den's
// own lines: a script printing thousands of lines would otherwise push den's
// warnings out of the panel.
type Logger struct {
	mu     sync.RWMutex
	own    []line
	output []line
	// seq numbers every line kept, across both buffers, so Lines can put
	// them back in the order they came; it is also the version the panel
	// watches, which keeps changing once the buffers are full.
	seq     uint64
	level   int
	fileOut io.WriteCloser
	// filePath is the file fileOut appends to, so a config reload that names
	// the same file keeps it open rather than opening it twice.
	filePath string
}

// line is one line as the TUI shows it, coloured once when it is logged
// rather than on every redraw.
type line struct {
	seq uint64
	tui string
}

var (
	timeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	dimTag     = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262"))
	infoTag    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C91BF"))
	warnTag    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5A623"))
	errorTag   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Bold(true)
	errorText  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	runbookTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#5BC8AF"))
)

func tagStyle(level int) lipgloss.Style {
	switch {
	case level <= LevelDebug:
		return dimTag
	case level == LevelInfo:
		return infoTag
	case level == LevelWarn:
		return warnTag
	default:
		return errorTag
	}
}

func New() *Logger { return &Logger{level: LevelInfo} }

// Configure sets the minimum log level and optionally opens a log file in append mode.
// It is called again on every config reload: a file no longer named is closed.
func (l *Logger) Configure(levelStr string, filePath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch strings.ToLower(levelStr) {
	case "trace":
		l.level = LevelTrace
	case "debug":
		l.level = LevelDebug
	case "warn", "warning":
		l.level = LevelWarn
	case "error":
		l.level = LevelError
	default:
		l.level = LevelInfo
	}

	if filePath == l.filePath {
		return nil
	}
	if l.fileOut != nil {
		_ = l.fileOut.Close()
		l.fileOut, l.filePath = nil, ""
	}
	if filePath != "" {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
			return fmt.Errorf("applog: create log dir: %w", err)
		}
		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("applog: open log file: %w", err)
		}
		l.fileOut, l.filePath = f, filePath
	}
	return nil
}

// Close flushes and closes the log file if open.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fileOut != nil {
		l.fileOut.Close()
		l.fileOut, l.filePath = nil, ""
	}
}

func (l *Logger) logAtLevel(msgLevel int, tag, msg string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if msgLevel < l.level {
		return
	}

	// Only den's own text goes through a style: the message is plain, so
	// the log view's grey applies to it, and an error stands out whole.
	text := msg
	if msgLevel >= LevelError {
		text = errorText.Render(msg)
	}
	l.own = l.keep(l.own, prefix(now, tagStyle(msgLevel).Render("["+tag+"]"))+text)

	if l.fileOut != nil {
		fmt.Fprintf(l.fileOut, "[%s] [%s] %s\n", now.Format("2006-01-02 15:04:05"), tag, msg)
	}
}

// Output keeps a line a run of runbook printed, already redacted, with the
// colours it printed. It counts as info, so a log level of warn or error
// leaves it out too. The file gets the words without the colour codes.
func (l *Logger) Output(runbook, msg string) {
	now := time.Now()
	// The panel is den's own screen: only colour may act on it.
	msg = process.KeepColours(msg)
	l.mu.Lock()
	defer l.mu.Unlock()

	if LevelInfo < l.level {
		return
	}

	l.output = l.keep(l.output, prefix(now, runbookTag.Render("["+runbook+"]"))+msg)

	if l.fileOut != nil {
		fmt.Fprintf(l.fileOut, "[%s] [RUN %s] %s\n", now.Format("2006-01-02 15:04:05"), runbook, ansi.Strip(msg))
	}
}

func prefix(t time.Time, tag string) string {
	return timeStyle.Render("["+t.Format("15:04:05")+"]") + " " + tag + " "
}

// keep appends a line to buf, dropping the oldest past maxLines. l.mu is held.
func (l *Logger) keep(buf []line, tui string) []line {
	l.seq++
	buf = append(buf, line{seq: l.seq, tui: tui})
	if len(buf) > maxLines {
		buf = buf[len(buf)-maxLines:]
	}
	return buf
}

// Trace logs a trace-level message (most verbose).
func (l *Logger) Trace(msg string) { l.logAtLevel(LevelTrace, "TRACE", msg) }

// Debug logs a debug-level message.
func (l *Logger) Debug(msg string) { l.logAtLevel(LevelDebug, "DEBUG", msg) }

// Info logs an info-level message.
func (l *Logger) Info(msg string) { l.logAtLevel(LevelInfo, "INFO", msg) }

// Warn logs a warning-level message.
func (l *Logger) Warn(msg string) { l.logAtLevel(LevelWarn, "WARN", msg) }

// Error logs an error-level message.
func (l *Logger) Error(msg string) { l.logAtLevel(LevelError, "ERROR", msg) }

// Log appends a timestamped message (alias for Info for backward compatibility).
func (l *Logger) Log(msg string) { l.Info(msg) }

// Write implements io.Writer — each write is treated as one log line.
func (l *Logger) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		l.Info(msg)
	}
	return len(p), nil
}

// Lines returns a snapshot of the stored lines, coloured for the TUI, in the
// order they were logged; with withOutput, the runs' output among them.
func (l *Logger) Lines(withOutput bool) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !withOutput {
		out := make([]string, len(l.own))
		for i, ln := range l.own {
			out[i] = ln.tui
		}
		return out
	}
	out := make([]string, 0, len(l.own)+len(l.output))
	i, j := 0, 0
	for i < len(l.own) || j < len(l.output) {
		if j == len(l.output) || (i < len(l.own) && l.own[i].seq < l.output[j].seq) {
			out = append(out, l.own[i].tui)
			i++
		} else {
			out = append(out, l.output[j].tui)
			j++
		}
	}
	return out
}

// Version changes whenever a line is kept, also once the buffers are full
// and their length no longer does.
func (l *Logger) Version() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.seq
}
