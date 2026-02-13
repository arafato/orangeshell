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
	ColorYellow    = lipgloss.Color("#EDD400")
	ColorRed       = lipgloss.Color("#EF2929")
	ColorBlue      = lipgloss.Color("#729FCF")
)

// Layout constants
const (
	// ContentMaxWidth can be used to cap content width on very wide terminals.
	// Currently unused — full width is used everywhere.
	ContentMaxWidth = 0
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

	// Log console styles (Workers tail)
	LogConsoleBg = lipgloss.Color("#111111")

	LogConsoleStyle = lipgloss.NewStyle().
			Background(LogConsoleBg)

	LogConsoleHeaderStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	LogTimestampStyle = lipgloss.NewStyle().
				Foreground(ColorGray)

	LogLevelLog = lipgloss.NewStyle().
			Foreground(ColorWhite)

	LogLevelWarn = lipgloss.NewStyle().
			Foreground(ColorOrange)

	LogLevelError = lipgloss.NewStyle().
			Foreground(ColorRed)

	LogLevelRequest = lipgloss.NewStyle().
			Foreground(ColorBlue)

	LogLevelSystem = lipgloss.NewStyle().
			Foreground(ColorGray).
			Italic(true)

	// D1 SQL console styles
	D1PromptStyle = lipgloss.NewStyle().
			Foreground(ColorOrange).
			Bold(true)

	D1TableBorderStyle = lipgloss.NewStyle().
				Foreground(ColorDarkGray)

	D1MetaStyle = lipgloss.NewStyle().
			Foreground(ColorGray).
			Italic(true)

	D1SchemaTitleStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	D1SchemaTableNameStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	D1SchemaColNameStyle = lipgloss.NewStyle().
				Foreground(ColorWhite)

	D1SchemaColTypeStyle = lipgloss.NewStyle().
				Foreground(ColorBlue)

	D1SchemaPKTagStyle = lipgloss.NewStyle().
				Foreground(ColorGreen).
				Bold(true)

	D1SchemaFKTagStyle = lipgloss.NewStyle().
				Foreground(ColorOrangeDim).
				Bold(true)

	D1SchemaNotNullStyle = lipgloss.NewStyle().
				Foreground(ColorRed)

	D1SchemaFKRefStyle = lipgloss.NewStyle().
				Foreground(ColorOrangeDim).
				Italic(true)

	D1SchemaBranchStyle = lipgloss.NewStyle().
				Foreground(ColorDarkGray)

	D1SchemaRelationStyle = lipgloss.NewStyle().
				Foreground(ColorGray)

	// Copy icon style (for clickable ID/Name fields)
	CopyIconStyle = lipgloss.NewStyle().
			Foreground(ColorGray)

	// Action popup styles
	ActionSectionStyle = lipgloss.NewStyle().
				Foreground(ColorOrange).
				Bold(true)

	ActionItemStyle = lipgloss.NewStyle().
			Foreground(ColorWhite)

	ActionDescStyle = lipgloss.NewStyle().
			Foreground(ColorGray)

	ActionDisabledStyle = lipgloss.NewStyle().
				Foreground(ColorDarkGray)

	ActionNavArrowStyle = lipgloss.NewStyle().
				Foreground(ColorOrangeDim)
)
