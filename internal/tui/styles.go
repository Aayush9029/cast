// Package tui holds shared lipgloss styles and keymap definitions used by both
// the interactive remote (`tv`) and the cast progress UI (`tv-cast`).
//
// Colors are dark-mode-first. We use `lipgloss.NormalBorder()` everywhere so
// every terminal renders consistent borders without falling back to Unicode
// box drawing.
package tui

import "github.com/charmbracelet/lipgloss"

// Palette keeps colors in one place. Foregrounds are picked to read well on a
// dark terminal; background `#1a1a1a` matches the spec for inset panels.
var (
	ColorBG       = lipgloss.Color("#1a1a1a")
	ColorBGSubtle = lipgloss.Color("#242424")
	ColorBorder   = lipgloss.Color("#3a3a3a")
	ColorAccent   = lipgloss.Color("#7dd3fc") // sky-300
	ColorActive   = lipgloss.Color("#fde68a") // amber-200
	ColorOK       = lipgloss.Color("#86efac") // green-300
	ColorWarn     = lipgloss.Color("#fca5a5") // red-300
	ColorMuted    = lipgloss.Color("#9ca3af") // gray-400
	ColorText     = lipgloss.Color("#e5e7eb") // gray-200
)

// Header is the resolved-TV banner shown at the top of every TUI.
var Header = lipgloss.NewStyle().
	Bold(true).
	Foreground(ColorText).
	Background(ColorBGSubtle).
	Padding(0, 1)

// StatusOK / StatusBad / StatusPending are inline status pills.
var (
	StatusOK = lipgloss.NewStyle().
			Foreground(ColorOK).
			Bold(true)
	StatusBad = lipgloss.NewStyle().
			Foreground(ColorWarn).
			Bold(true)
	StatusPending = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true)
)

// Panel wraps the central content area (D-pad, progress bar, list).
var Panel = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	BorderForeground(ColorBorder).
	Padding(1, 2).
	Background(ColorBG)

// Key represents an idle D-pad / hint key.
var Key = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	BorderForeground(ColorBorder).
	Foreground(ColorText).
	Padding(0, 1).
	Background(ColorBG).
	Width(7).
	Align(lipgloss.Center)

// KeyActive is the highlighted (just-pressed) variant.
var KeyActive = Key.
	BorderForeground(ColorActive).
	Foreground(ColorActive).
	Bold(true)

// KeySpacer is a same-size invisible cell used to grid out the D-pad.
var KeySpacer = lipgloss.NewStyle().Width(7).Padding(0, 1)

// Sidebar wraps the recent-events list.
var Sidebar = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	BorderForeground(ColorBorder).
	Padding(0, 1).
	Foreground(ColorMuted).
	Background(ColorBG)

// Footer holds the keybinding hint row.
var Footer = lipgloss.NewStyle().
	Foreground(ColorMuted).
	Padding(0, 1)

// Hint formats a single one-letter binding hint, e.g. "[p] power".
var Hint = lipgloss.NewStyle().
	Foreground(ColorAccent)

// HintMuted is for non-current/disabled hints.
var HintMuted = lipgloss.NewStyle().
	Foreground(ColorMuted)

// Modal is the centered confirm dialog used for destructive actions.
var Modal = lipgloss.NewStyle().
	Border(lipgloss.DoubleBorder()).
	BorderForeground(ColorWarn).
	Padding(1, 3).
	Background(ColorBG).
	Foreground(ColorText)

// TitleBar is the section title strip inside panels.
var TitleBar = lipgloss.NewStyle().
	Foreground(ColorAccent).
	Bold(true).
	Padding(0, 1)
