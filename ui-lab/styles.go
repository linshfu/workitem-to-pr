package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ---- palette ----
var (
	accent = lipgloss.Color("#78AFFF")
	muted  = lipgloss.Color("#7A7C93")
	dim    = lipgloss.Color("#4E5670")
	selBg  = lipgloss.Color("#264A9E")
	selFg  = lipgloss.Color("#ECF0FA")
	okCol  = lipgloss.Color("#34D399")
	errCol = lipgloss.Color("#F87171")
)

func styleFg(c lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func styleBold(c lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

// ---- brand name (blue -> cyan gradient) ----
func lerp(a, b, t float64) int { return int(a + (b-a)*t) }

func brand(s string) string {
	from := [3]float64{0x4A, 0x90, 0xE2} // blue
	to := [3]float64{0x5E, 0xEA, 0xD4}   // teal/cyan
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		col := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
			lerp(from[0], to[0], t), lerp(from[1], to[1], t), lerp(from[2], to[2], t)))
		b.WriteString(lipgloss.NewStyle().Foreground(col).Bold(true).Render(string(r)))
	}
	return b.String()
}

// fuzzyMatch reports whether query is a subsequence of target (case-insensitive).
func fuzzyMatch(query, target string) bool {
	query = strings.ToLower(query)
	target = strings.ToLower(target)
	qr := []rune(query)
	qi := 0
	for _, tr := range target {
		if qi < len(qr) && tr == qr[qi] {
			qi++
		}
	}
	return qi == len(qr)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
