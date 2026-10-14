package main

import (
	"math/rand/v2"
	"strings"

	"charm.land/lipgloss/v2"
)

// The TUI's launch splash: AISECENG in block letters, echoing the site's 404
// art, decoding once from shade glyphs into the site's callout gradient.

var blockFont = map[rune][5]string{
	'A': {" ███ ", "█   █", "█████", "█   █", "█   █"},
	'I': {"███", " █ ", " █ ", " █ ", "███"},
	'S': {" ████", "█    ", " ███ ", "    █", "████ "},
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'C': {" ████", "█    ", "█    ", "█    ", " ████"},
	'N': {"█   █", "██  █", "█ █ █", "█  ██", "█   █"},
	'G': {" ████", "█    ", "█  ██", "█   █", " ████"},
}

// bannerRows is AISECENG as five rows of block glyphs, one space between
// letters.
var bannerRows = func() [5][]rune {
	var rows [5][]rune

	for i, letter := range "AISECENG" {
		for r := range rows {
			if i > 0 {
				rows[r] = append(rows[r], ' ')
			}

			rows[r] = append(rows[r], []rune(blockFont[letter][r])...)
		}
	}

	return rows
}()

var bannerWidth = len(bannerRows[0])

// decodeFront is how many columns of shade glyphs run ahead of the decoded
// part.
const decodeFront = 8

// renderBanner draws the banner with `reveal` (0 to 1) of its columns decoded,
// left to right. rng picks the shade glyphs, so each frame shimmers differently.
func renderBanner(reveal float64, rng *rand.Rand) string {
	gradient := lipgloss.Blend1D(bannerWidth, cobalt, ultraviolet, coral)
	cut := int(reveal * float64(bannerWidth+decodeFront))
	shades := []rune("░▒▓")
	lines := make([]string, len(bannerRows))

	for r, row := range bannerRows {
		var b strings.Builder

		for c, glyph := range row {
			switch {
			case glyph == ' ' || c >= cut:
				b.WriteRune(' ')
			case c < cut-decodeFront:
				b.WriteString(lipgloss.NewStyle().Foreground(gradient[c]).Render(string(glyph)))
			default:
				b.WriteString(mutedText.Render(string(shades[rng.IntN(len(shades))])))
			}
		}

		lines[r] = b.String()
	}

	return strings.Join(lines, "\n")
}
