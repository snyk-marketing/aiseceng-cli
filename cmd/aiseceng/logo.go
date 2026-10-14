package main

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// The site's logo mark for the TUI's top left corner: the A's left leg and
// the two strokes beside it, as 20x8 pixels.
var logoPixels = [...]string{
	".......##...........",
	"......###...###.....",
	".....###.##..###....",
	"....###..###..###...",
	"...###....###..###..",
	"..###......###..###.",
	".###........###..###",
	"###.................",
}

// logoRows is the mark in quadrant blocks, two by two pixels to a cell, each
// row a step down the logo's gradient from the yellow apex to the cobalt foot.
var logoRows = func() []string {
	quadrants := []rune(" ▘▝▀▖▌▞▛▗▚▐▜▄▙▟█")
	colors := []color.Color{sparkYellow, coral, ultraviolet, cobalt}
	rows := make([]string, len(logoPixels)/2)

	for r := range rows {
		top, bottom := logoPixels[2*r], logoPixels[2*r+1]

		var b strings.Builder

		for c := 0; c < len(top); c += 2 {
			var quadrant int

			for bit, pixel := range [4]byte{top[c], top[c+1], bottom[c], bottom[c+1]} {
				if pixel == '#' {
					quadrant |= 1 << bit
				}
			}

			b.WriteRune(quadrants[quadrant])
		}

		rows[r] = lipgloss.NewStyle().Foreground(colors[r]).Render(b.String())
	}

	return rows
}()

var logoWidth = len(logoPixels[0]) / 2
