package tui

// Hints describes one keybinding for the footer row.
type HintItem struct {
	Key   string
	Label string
}

// FormatHints lays out hints inline using the Hint/HintMuted styles.
func FormatHints(items []HintItem) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += "  "
		}
		out += Hint.Render("["+it.Key+"]") + HintMuted.Render(" "+it.Label)
	}
	return out
}
