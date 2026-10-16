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

// sea is the empty braille cell, which every cell starts as.
const sea = '\u2800'

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
			glyph := sea

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

// mapCell is the cell a position falls in, clamped onto the map. A coastal
// city whose cell the rough outlines leave as sea moves to the nearest
// neighbouring cell with land, so its pin never floats offshore.
func (m *landMask) mapCell(lat, lon float64) (x, y int) {
	fx := (lon + 180) / 360 * float64(m.width)
	fy := (mapNorth - lat) / (mapNorth - mapSouth) * float64(m.height)
	x, y = min(max(int(fx), 0), m.width-1), min(max(int(fy), 0), m.height-1)

	if m.cells[y][x] != sea {
		return x, y
	}

	bestX, bestY, best := x, y, math.Inf(1)

	for ny := max(y-1, 0); ny <= min(y+1, m.height-1); ny++ {
		for nx := max(x-1, 0); nx <= min(x+1, m.width-1); nx++ {
			// A cell is about twice as tall as it is wide.
			d := math.Hypot(float64(nx)+0.5-fx, 2*(float64(ny)+0.5-fy))

			if m.cells[ny][nx] != sea && d < best {
				bestX, bestY, best = nx, ny, d
			}
		}
	}

	return bestX, bestY
}

// mapFrame is what one frame of the map shows.
type mapFrame struct {
	// reveal is how much of the land is drawn, west to east, from 0 to 1.
	reveal float64
	// lit is how many chapters are lit, west to east.
	lit int
	// highlight is the chapter marked in coral, with its name beside it.
	highlight *Chapter
	// ring is the radius of the ping around the highlight; 0 draws none.
	ring int
}

// mapGlyph is one cell of a rendered map: its glyph and the style it takes.
type mapGlyph struct {
	glyph rune
	style *lipgloss.Style
}

var (
	// pinStyles is a chapter pin's style per region; see regionColors.
	pinStyles   = map[string]*lipgloss.Style{}
	markerStyle = linkText.Bold(true)
)

func init() {
	for region, c := range regionColors {
		style := lipgloss.NewStyle().Foreground(c)
		pinStyles[region] = &style
	}
}

// pinStyle is a chapter's pin in its region's colour, or white.
func pinStyle(c Chapter) *lipgloss.Style {
	return cmp.Or(pinStyles[deref(c.Region)], &whiteText)
}

func (m *landMask) render(chapters []Chapter, frame mapFrame) []string {
	grid := make([][]mapGlyph, m.height)

	for y := range grid {
		grid[y] = make([]mapGlyph, m.width)

		for x, glyph := range m.cells[y] {
			if float64(x) >= frame.reveal*float64(m.width) {
				glyph = ' '
			}

			grid[y][x] = mapGlyph{glyph, &landText}
		}
	}

	westToEast := slices.SortedFunc(slices.Values(chapters), func(a, b Chapter) int {
		return cmp.Compare(a.Lon, b.Lon)
	})

	for _, c := range westToEast[:min(frame.lit, len(westToEast))] {
		x, y := m.mapCell(c.Lat, c.Lon)
		grid[y][x] = mapGlyph{'●', pinStyle(c)}
	}

	if c := frame.highlight; c != nil {
		x, y := m.mapCell(c.Lat, c.Lon)

		if r := frame.ring; r > 0 {
			for _, d := range [][2]int{{-2 * r, 0}, {2 * r, 0}, {0, -r}, {0, r}, {-r, -1}, {r, -1}, {-r, 1}, {r, 1}} {
				if px, py := x+d[0], y+d[1]; px >= 0 && px < m.width && py >= 0 && py < m.height {
					grid[py][px] = mapGlyph{'·', &linkText}
				}
			}
		}

		grid[y][x] = mapGlyph{'◉', &markerStyle}

		for i, letter := range []rune(" " + c.City()) {
			if px := x + 1 + i; px < m.width {
				grid[y][px] = mapGlyph{letter, &boldText}
			}
		}
	}

	lines := make([]string, m.height)
	for y, row := range grid {
		lines[y] = renderRuns(row)
	}

	return lines
}

// renderRuns renders a row of the map with one Render per run of cells that
// share a style, rather than one per cell: the land is most of every row.
func renderRuns(row []mapGlyph) string {
	var line, run strings.Builder

	for x, cell := range row {
		run.WriteRune(cell.glyph)

		if x == len(row)-1 || row[x+1].style != cell.style {
			line.WriteString(cell.style.Render(run.String()))
			run.Reset()
		}
	}

	return line.String()
}
