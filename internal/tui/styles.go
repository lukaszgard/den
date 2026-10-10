package tui

import "charm.land/lipgloss/v2"

var (
	colorGreen  = lipgloss.Color("#04B575")
	colorYellow = lipgloss.Color("#ECFD65")
	colorRed    = lipgloss.Color("#FF5F87")
	colorBlue   = lipgloss.Color("#6C91BF")
	colorGray   = lipgloss.Color("#626262")
	colorWhite  = lipgloss.Color("#FFFDF5")
	colorSubtle = lipgloss.Color("#9B9B9B")

	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(lipgloss.Color("#1a1a2e")).
			Padding(0, 2)

	StyleStatusConnected  = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	StyleStatusIdle       = lipgloss.NewStyle().Foreground(colorGray)
	StyleStatusError      = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	StyleStatusConnecting = lipgloss.NewStyle().Foreground(colorYellow)
	StyleStatusStopping   = lipgloss.NewStyle().Foreground(colorYellow)

	StyleBadgeRDS    = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
	StyleBadgeKafka  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	StyleBadgeRedis  = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	StyleBadgeDocker = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71")).Bold(true)

	StylePort = lipgloss.NewStyle().Foreground(colorSubtle)

	StyleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(0, 1)

	StyleLogLine  = lipgloss.NewStyle().Foreground(colorSubtle)
	StyleSelected = lipgloss.NewStyle().Foreground(colorWhite).Bold(true)

	StyleHelp = lipgloss.NewStyle().Foreground(colorGray)
	StyleKey  = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)

	StyleConnectCmd = lipgloss.NewStyle().
			Foreground(colorYellow).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 1)

	StyleTokenBarFill  = lipgloss.NewStyle().Foreground(colorGreen)
	StyleTokenBarEmpty = lipgloss.NewStyle().Foreground(colorGray)
	StyleTokenWarning  = lipgloss.NewStyle().Foreground(colorYellow)
	StyleTokenExpired  = lipgloss.NewStyle().Foreground(colorRed)
)
