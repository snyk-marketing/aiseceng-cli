package main

import (
	"cmp"
	"image/color"
	"math"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// The chapter map: the dot-matrix world from aiseceng.io's homepage, drawn in
// braille, two dots across and four down per cell, with one lit dot per
// chapter in its region's colour. Equirectangular, cropped to 83°N to 55°S
// where the land is, like the site's.

// landOutlines are the continents as rough [lon, lat] outlines, copied from
// the site's map so the two match. Rough is all a terminal-sized map can show.
var landOutlines = [][][2]float64{
	// North America
	{{-168, 66}, {-160, 70}, {-140, 70}, {-120, 72}, {-95, 72}, {-80, 70}, {-62, 60}, {-55, 52}, {-66, 44}, {-76, 36}, {-81, 25}, {-97, 26}, {-105, 22}, {-95, 16}, {-85, 10}, {-78, 8}, {-90, 14}, {-106, 24}, {-118, 32}, {-124, 40}, {-125, 50}, {-135, 58}, {-150, 60}, {-165, 60}},
	// Greenland
	{{-55, 60}, {-45, 60}, {-20, 70}, {-25, 82}, {-60, 82}, {-72, 76}},
	// South America
	{{-78, 8}, {-60, 10}, {-50, 0}, {-35, -6}, {-40, -22}, {-48, -28}, {-58, -38}, {-66, -46}, {-72, -52}, {-75, -40}, {-71, -18}, {-80, -5}},
	// Europe
	{{-10, 36}, {-9, 43}, {-2, 48}, {5, 52}, {8, 58}, {5, 62}, {15, 69}, {28, 71}, {40, 67}, {40, 55}, {30, 46}, {28, 41}, {22, 37}, {12, 38}, {8, 44}, {0, 38}},
	// Africa
	{{-17, 21}, {-10, 30}, {-5, 36}, {10, 37}, {32, 31}, {35, 28}, {43, 12}, {51, 12}, {40, -5}, {40, -15}, {35, -25}, {20, -35}, {12, -18}, {9, -1}, {8, 5}, {-8, 4}, {-17, 14}},
	// Asia
	{{28, 41}, {40, 55}, {40, 67}, {60, 70}, {100, 78}, {140, 72}, {170, 68}, {180, 65}, {160, 58}, {142, 48}, {130, 35}, {122, 30}, {120, 22}, {108, 10}, {104, 1}, {100, 13}, {92, 20}, {80, 8}, {72, 20}, {67, 25}, {57, 25}, {50, 30}, {44, 12}, {35, 30}, {28, 36}},
	// Australia
	{{114, -22}, {122, -18}, {136, -12}, {142, -11}, {153, -26}, {150, -37}, {140, -38}, {130, -32}, {115, -34}},
	// British Isles
	{{-6, 50}, {1, 51}, {-2, 58}, {-6, 57}},
	// Japan
	{{130, 32}, {140, 36}, {142, 44}, {138, 38}},
	// New Zealand
	{{172, -35}, {178, -38}, {170, -46}},
}

const mapNorth, mapSouth = 83.0, -55.0

// regionColors match the site's map legend: Americas ultraviolet, EMEA coral,
// APAC spark yellow. A region missing here lights in white.
var regionColors = map[string]color.Color{"americas": ultraviolet, "emea": coral, "apac": sparkYellow}

// contains is even-odd ray casting, as the site's map does it.
func contains(outline [][2]float64, lon, lat float64) bool {
	inside := false

	for i, j := 0, len(outline)-1; i < len(outline); j, i = i, i+1 {
		xi, yi := outline[i][0], outline[i][1]
		xj, yj := outline[j][0], outline[j][1]

		if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}

	return inside
}

// brailleBits is the dot each [row][column] of a braille cell sets.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// landMask is the land drawn for one map size, as one braille rune per cell.
// The land never moves, so a size is worked out once and kept.
type landMask struct {
	width, height int
	cells         [][]rune
}

func newLandMask(width, height int) *landMask {
	cells := make([][]rune, height)

	for y := range cells {
		cells[y] = make([]rune, width)

		for x := range cells[y] {
			glyph := rune(0x2800)

			for dy := range 4 {
				for dx := range 2 {
					lon := -180 + (float64(2*x+dx)+0.5)*360/float64(2*width)
					lat := mapNorth - (float64(4*y+dy)+0.5)*(mapNorth-mapSouth)/float64(4*height)

					if slices.ContainsFunc(landOutlines, func(outline [][2]float64) bool { return contains(outline, lon, lat) }) {
						glyph |= brailleBits[dy][dx]
					}
				}
			}

			cells[y][x] = glyph
		}
	}

	return &landMask{width, height, cells}
}

// mapCell is the cell a position falls in, clamped onto the map.
func (m *landMask) mapCell(lat, lon float64) (x, y int) {
	x = int((lon + 180) / 360 * float64(m.width))
	y = int((mapNorth - lat) / (mapNorth - mapSouth) * float64(m.height))

	return min(max(x, 0), m.width-1), min(max(y, 0), m.height-1)
}

// mapFrame is what one frame of the map shows.
type mapFrame struct {
	// reveal is how much of the land is drawn, west to east, from 0 to 1.
	reveal float64
	// lit is how many chapters are lit, west to east.
	lit int
	// highlight is the chapter whose name rides beside the marker.
	highlight *Chapter
	// flight is the marker, its trail and its landing ring; nil draws none.
	flight *flight
}

// fades are the trail's and the ring's colours as they age, coral to land.
var (
	trailFade = lipgloss.Blend1D(trailFrames, coral, land)
	ringFade  = lipgloss.Blend1D(ringFrames, coral, land)
)

func (m *landMask) render(chapters []Chapter, frame mapFrame) []string {
	grid := make([][]string, m.height)

	for y := range grid {
		grid[y] = make([]string, m.width)

		for x, glyph := range m.cells[y] {
			if float64(x) >= frame.reveal*float64(m.width) {
				grid[y][x] = " "
			} else {
				grid[y][x] = landText.Render(string(glyph))
			}
		}
	}

	westToEast := slices.SortedFunc(slices.Values(chapters), func(a, b Chapter) int {
		return cmp.Compare(a.Lon, b.Lon)
	})

	for _, c := range westToEast[:min(frame.lit, len(westToEast))] {
		x, y := m.mapCell(c.Lat, c.Lon)
		grid[y][x] = lipgloss.NewStyle().Foreground(cmp.Or(regionColors[deref(c.Region)], white)).Render("●")
	}

	if c, f := frame.highlight, frame.flight; c != nil && f != nil && f.slug != "" {
		for _, dot := range f.trail {
			x, y := m.mapCell(dot.lat, dot.lon)
			grid[y][x] = lipgloss.NewStyle().Foreground(trailFade[dot.age]).Render("·")
		}

		x, y := m.mapCell(f.lat.pos, f.lon.pos)

		if r := int(math.Round(f.ring.pos)); f.ringLeft > 0 && r > 0 {
			dot := lipgloss.NewStyle().Foreground(ringFade[ringFrames-f.ringLeft]).Render("·")

			for _, d := range [][2]int{{-2 * r, 0}, {2 * r, 0}, {0, -r}, {0, r}, {-r, -1}, {r, -1}, {-r, 1}, {r, 1}} {
				if px, py := x+d[0], y+d[1]; px >= 0 && px < m.width && py >= 0 && py < m.height {
					grid[py][px] = dot
				}
			}
		}

		grid[y][x] = linkText.Bold(true).Render("◉")

		for i, letter := range " " + c.City() {
			if px := x + 1 + i; px < m.width {
				grid[y][px] = boldText.Render(string(letter))
			}
		}
	}

	lines := make([]string, m.height)
	for y, row := range grid {
		lines[y] = strings.Join(row, "")
	}

	return lines
}
