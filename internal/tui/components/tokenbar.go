package components

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const barWidth = 22

// TokenBar renders a progress bar showing the remaining RDS token TTL.
// tokenExpires: when the token expires. width: available terminal width.
func TokenBar(tokenExpires *time.Time, width int) string {
	return TokenBarWithTotal(tokenExpires, 15*time.Minute, width)
}

// TokenBarWithTotal renders a progress bar with a caller-supplied total duration.
// tokenExpires: when the token expires. total: the full duration of the token.
// width: available terminal width.
func TokenBarWithTotal(tokenExpires *time.Time, total time.Duration, width int) string {
	if tokenExpires == nil {
		return ""
	}
	remaining := max(time.Until(*tokenExpires), 0)

	pct := float64(remaining) / float64(total)
	if pct > 1 {
		pct = 1
	}

	bw := barWidth
	if width > 60 {
		bw = width / 3
	}
	filled := int(pct * float64(bw))
	empty := bw - filled

	var barStyle lipgloss.Style
	switch {
	case remaining < 2*time.Minute:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	case remaining < 5*time.Minute:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECFD65"))
	default:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	}

	bar := barStyle.Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render(strings.Repeat("░", empty))

	hours := int(remaining.Hours())
	mins := int(remaining.Minutes()) % 60
	secs := int(remaining.Seconds()) % 60
	var timeStr string
	if hours > 0 {
		timeStr = fmt.Sprintf("%dh %02dm remaining", hours, mins)
	} else {
		timeStr = fmt.Sprintf("%dm %02ds remaining", mins, secs)
	}
	return fmt.Sprintf("[%s] %s", bar, timeStr)
}
