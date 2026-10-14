package main

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
)

// The TUI's keys, named once: Update matches against these bindings, and the
// key bar and the `?` reference print from them, so the three always agree.
// A binding's help key is its label in the reference; its help text is its
// shortest description, for the key bar.

type keyMap struct {
	Up, Down, Top, Bottom, PageUp, PageDown key.Binding
	ScrollUp, ScrollDown                    key.Binding
	NextTab, PrevTab, JumpTab               key.Binding
	Search, Clear                           key.Binding
	Open, Luma, Meetup, Copy                key.Binding
	Retry, Help, Quit, ForceQuit            key.Binding
}

var keys = keyMap{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑ k", "up")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓ j", "down")),
	Top:        key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top")),
	Bottom:     key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom")),
	PageUp:     key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
	PageDown:   key.NewBinding(key.WithKeys("pgdown", "space"), key.WithHelp("pgdn", "page down")),
	ScrollUp:   key.NewBinding(key.WithKeys("shift+up", "K"), key.WithHelp("⇧↑", "scroll up")),
	ScrollDown: key.NewBinding(key.WithKeys("shift+down", "J"), key.WithHelp("⇧↓", "scroll down")),
	NextTab:    key.NewBinding(key.WithKeys("tab", "right"), key.WithHelp("tab →", "next tab")),
	PrevTab:    key.NewBinding(key.WithKeys("shift+tab", "left"), key.WithHelp("⇧tab ←", "previous tab")),
	JumpTab:    key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "jump to a tab")),
	Search:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Clear:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
	Open:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
	Luma:       key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "Luma")),
	Meetup:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "Meetup")),
	Copy:       key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy link")),
	Retry:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry")),
	Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "keys")),
	Quit:       key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	ForceQuit:  key.NewBinding(key.WithKeys("ctrl+c")),
}

// typingKeyMap is what the keys do while the search has focus; every other
// key types.
type typingKeyMap struct {
	Up, Down, Done, Clear, NextTab, PrevTab key.Binding
}

var typingKeys = typingKeyMap{
	Up:      key.NewBinding(key.WithKeys("up", "ctrl+p")),
	Down:    key.NewBinding(key.WithKeys("down", "ctrl+n")),
	Done:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "done")),
	Clear:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
	NextTab: key.NewBinding(key.WithKeys("tab")),
	PrevTab: key.NewBinding(key.WithKeys("shift+tab")),
}

// listingKey is the binding that opens an event's listing: l for Luma, m
// for Meetup. Anywhere else is reached through enter, when it is the RSVP.
func listingKey(link EventLink) (key.Binding, bool) {
	switch link.Label {
	case "Luma":
		return keys.Luma, true
	case "Meetup":
		return keys.Meetup, true
	}

	return key.Binding{}, false
}

// newKeyBar is the help bubble the key bar renders with, in the site's
// colours: keys white, what they do muted.
func newKeyBar() help.Model {
	bar := help.New()
	bar.ShortSeparator = "   "
	bar.Styles.ShortKey = whiteText
	bar.Styles.ShortDesc = mutedText
	bar.Styles.Ellipsis = mutedText

	return bar
}

// relabel is a copy of b with other help, for a key bar that says it shorter
// or names what it does on this tab.
func relabel(b key.Binding, label, desc string) key.Binding {
	b.SetHelp(label, desc)

	return b
}

// referenceRow is one line of the `?` reference: the keys of its bindings,
// side by side, and what they do.
type referenceRow struct {
	bindings []key.Binding
	desc     string
}

// keyReference is the `?` reference in groups, headed in the detail pane's
// label style.
var keyReference = []struct {
	head string
	rows []referenceRow
}{
	{"Move", []referenceRow{
		{[]key.Binding{keys.Up, keys.Down}, "move"},
		{[]key.Binding{keys.Top, keys.Bottom}, "top, bottom"},
		{[]key.Binding{keys.PageUp, keys.PageDown}, "page"},
	}},
	{"Search", []referenceRow{
		{[]key.Binding{keys.Search}, "search as you type"},
		{[]key.Binding{keys.Clear}, "clear the search"},
	}},
	{"Open", []referenceRow{
		{[]key.Binding{keys.Open}, "rsvp, apply, chapter page"},
		{[]key.Binding{keys.Luma, keys.Meetup}, "on Luma, on Meetup"},
		{[]key.Binding{keys.Copy}, "copy the link"},
	}},
	{"Detail", []referenceRow{
		{[]key.Binding{keys.ScrollUp, keys.ScrollDown}, "scroll, or use the wheel"},
	}},
	{"Tabs", []referenceRow{
		{[]key.Binding{keys.NextTab, keys.PrevTab}, "next, previous"},
		{[]key.Binding{keys.JumpTab}, "jump"},
	}},
	{"", []referenceRow{
		{[]key.Binding{keys.Retry}, "retry a failed load"},
		{[]key.Binding{keys.Help}, "close this"},
		{[]key.Binding{keys.Quit}, "quit"},
	}},
}

// referenceLines is the `?` reference, one line per row.
func referenceLines() []string {
	const keyWidth = 16

	var lines []string

	for i, group := range keyReference {
		if i > 0 {
			lines = append(lines, "")
		}

		if group.head != "" {
			lines = append(lines, labelText.Render(strings.ToUpper(group.head)))
		}

		for _, row := range group.rows {
			labels := make([]string, len(row.bindings))
			for j, b := range row.bindings {
				labels[j] = b.Help().Key
			}

			lines = append(lines, whiteText.Render(pad(strings.Join(labels, "  "), keyWidth))+secondaryText.Render(row.desc))
		}
	}

	return lines
}
