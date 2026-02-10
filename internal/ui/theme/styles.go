package theme

import "github.com/charmbracelet/lipgloss"

// Color palette — Cloudflare-inspired orange accent with neutral grays.
var (
	ColorOrange    = lipgloss.Color("#F6821F")
	ColorOrangeDim = lipgloss.Color("#C46A1A")
	ColorWhite     = lipgloss.Color("#FAFAFA")
	ColorGray      = lipgloss.Color("#7D7D7D")
	ColorDarkGray  = lipgloss.Color("#3A3A3A")
	ColorBg        = lipgloss.Color("#1A1A2E")
	ColorBgLight   = lipgloss.Color("#222240")
	ColorGreen     = lipgloss.Color("#73D216")
	ColorRed       = lipgloss.Color("#EF2929")
	ColorBlue      = lipgloss.Color("#729FCF")
)

// Layout constants
const (
	SidebarMinWidth = 22
	SidebarMaxWidth = 30
	SidebarRatio    = 0.20 // 20% of terminal width
)

// Shared styles
var (
	// Border styles
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorDarkGray)

	ActiveBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorOrange)

	// Text styles
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorOrange)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorGray)

	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorBlue).
			Bold(true)

	ValueStyle = lipgloss.NewStyle().
			Foreground(ColorWhite)

	DimStyle = lipgloss.NewStyle().
			Foreground(ColorGray)

	// Status styles
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorGreen)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorRed)

	// Header bar
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorWhite).
			Background(ColorOrangeDim).
			Padding(0, 1)

	// Help bar
	HelpBarStyle = lipgloss.NewStyle().
			Foreground(ColorGray).
			Padding(0, 1)

	// Selection indicator
	SelectedItemStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	NormalItemStyle = lipgloss.NewStyle().
			Foreground(ColorWhite)
)
