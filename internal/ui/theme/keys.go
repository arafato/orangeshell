package theme

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines all key bindings for the application.
type KeyMap struct {
	Up          key.Binding
	Down        key.Binding
	Enter       key.Binding
	Back        key.Binding
	Tab         key.Binding
	Search      key.Binding
	Actions     key.Binding
	PrevAccount key.Binding
	NextAccount key.Binding
	Tail        key.Binding
	Quit        key.Binding
	Help        key.Binding
	Escape      key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("up/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("down/j", "down"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("backspace", "esc"),
			key.WithHelp("esc", "back"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch pane"),
		),
		Search: key.NewBinding(
			key.WithKeys("ctrl+k", "/"),
			key.WithHelp("ctrl+k", "search"),
		),
		Actions: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "actions"),
		),
		PrevAccount: key.NewBinding(
			key.WithKeys("["),
			key.WithHelp("[/]", "account"),
		),
		NextAccount: key.NewBinding(
			key.WithKeys("]"),
			key.WithHelp("[/]", "account"),
		),
		Tail: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "tail"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "close"),
		),
	}
}

// ShortHelp returns key bindings shown in the mini help bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Tab, k.Search, k.Actions, k.Tail, k.PrevAccount, k.Quit}
}

// FullHelp returns all key bindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Tab, k.Search, k.Actions, k.Tail, k.PrevAccount, k.Help, k.Quit},
	}
}
