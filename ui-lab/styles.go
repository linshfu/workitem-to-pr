package main

import (
	"fmt"
	"strconv"
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

// ---- wizard step tracker（/pbi、/task、/release 共用）----

type wizStep struct {
	label   string
	val     string // 已選/已填的值，顯示在步驟名後
	mutates bool   // 這步執行後會真的建立/寫入實體（建單、建分支、建 PR、push…）
}

// stepsView 一次列出所有步驟、標出目前在哪一步（✓ 完成 / ▶ 現在 / ○ 未到），
// 並在會實際寫入的步驟標 ⚠。cur 是目前第幾步（1-based）。
func stepsView(steps []wizStep, cur int) string {
	var b strings.Builder
	for i, s := range steps {
		n := i + 1
		var mark, name string
		switch {
		case n < cur:
			mark, name = styleFg(okCol, "✓"), styleFg(muted, s.label)
		case n == cur:
			mark, name = styleFg(accent, "▶"), styleFg(accent, s.label)
		default:
			mark, name = styleFg(dim, "○"), styleFg(dim, s.label)
		}
		line := " " + mark + " " + styleFg(dim, strconv.Itoa(n)+".") + " " + name
		if s.val != "" {
			line += "  " + styleFg(muted, s.val)
		}
		if s.mutates {
			line += "  " + styleFg(errCol, "⚠ 會實際寫入")
		}
		b.WriteString(line + "\n")
	}
	return b.String()
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

// wrapRunes 把字串折成每行顯示寬度不超過 w 的多行（中文字寬 2，用 lipgloss.Width 計算）。
// 空字串回傳 nil，呼叫端可直接 range 而不必先判空。
func wrapRunes(s string, w int) []string {
	if strings.TrimSpace(s) == "" || w <= 0 {
		return nil
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if curW+rw > w && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
