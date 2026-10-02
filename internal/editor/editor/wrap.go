package editor

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// wrapRunes wraps text to width w, word-wise, hard-breaking words longer
// than the width. Newlines are preserved as paragraph breaks.
func wrapRunes(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		cur := ""
		for _, word := range strings.Split(para, " ") {
			if r := []rune(word); len(r) > w {
				if cur != "" { // flush pending line before hard-breaking
					out = append(out, cur)
					cur = ""
				}
				for len(r) > w {
					out = append(out, string(r[:w]))
					r = r[w:]
				}
				word = string(r)
			}
			switch {
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				out = append(out, cur)
				cur = word
			}
		}
		out = append(out, cur)
	}
	return out
}
