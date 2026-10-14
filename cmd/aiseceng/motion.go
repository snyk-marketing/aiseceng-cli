package main

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

// The TUI's spring motion, on harmonica's damped springs: the coral rule
// stretches to a new tab, the cursor bar glides between rows as the list
// scrolls, and the map's marker flies to the selected city. A spring can be
// retargeted mid-move, so holding a key keeps the motion continuous, and each
// settles within a second, after which the frame clock stops.

var (
	// The rule's leading edge runs ahead of its trailing edge, so it stretches.
	leadSpring  = newSpring(22, 0.75)
	trailSpring = newSpring(12, 0.9)
	// The bar overshoots a little; the scroll never does.
	cursorSpring = newSpring(24, 0.72)
	scrollSpring = newSpring(16, 1)
	flightSpring = newSpring(8, 0.72)
	ringSpring   = newSpring(14, 0.55)
)

const (
	// trailFrames is how long a dot of the marker's trail takes to fade.
	trailFrames = 12
	// ringFrames is how long the ring sent out on landing takes to fade.
	ringFrames = 14
	// landingDegrees is how close the marker gets before it counts as landed.
	landingDegrees = 3
	// nearCell and nearDegree are how close a glide in cells, or in degrees,
	// gets before it snaps home: closer than the screen can show, since a map
	// cell is six degrees or more.
	nearCell   = 0.1
	nearDegree = 1
)

// newSpring is a harmonica spring stepped once per frame.
func newSpring(angularFrequency, dampingRatio float64) harmonica.Spring {
	return harmonica.NewSpring(frameInterval.Seconds(), angularFrequency, dampingRatio)
}

// glide is a value easing toward its target on a spring. It lands exactly on
// the target once within `near` of it and moving less than that a frame, so
// a settled glide reads as not moving.
type glide struct {
	pos, vel, target float64
	spring           harmonica.Spring
	near             float64
}

func newGlide(spring harmonica.Spring, near float64) glide {
	return glide{spring: spring, near: near}
}

func (g *glide) step() {
	g.pos, g.vel = g.spring.Update(g.pos, g.vel, g.target)

	if math.Abs(g.pos-g.target) < g.near && math.Abs(g.vel)*frameInterval.Seconds() < g.near {
		g.snap(g.target)
	}
}

func (g *glide) snap(to float64) {
	g.pos, g.vel, g.target = to, 0, to
}

func (g glide) moving() bool {
	return g.pos != g.target || g.vel != 0
}

// tabRule is the coral rule under the active tab, as its two edges in cells.
// At rest the tab bar draws it under the active tab directly.
type tabRule struct{ left, right glide }

func newTabRule() tabRule {
	return tabRule{left: newGlide(trailSpring, nearCell), right: newGlide(leadSpring, nearCell)}
}

// slide starts the rule from tab `from` toward tab `to`, or turns it there
// mid-slide.
func (r *tabRule) slide(spans [][2]int, from, to tab) {
	if !r.moving() {
		r.left.snap(float64(spans[from][0]))
		r.right.snap(float64(spans[from][1]))
	}

	r.left.spring, r.right.spring = leadSpring, trailSpring
	if to > from {
		r.left.spring, r.right.spring = trailSpring, leadSpring
	}

	r.left.target, r.right.target = float64(spans[to][0]), float64(spans[to][1])
}

func (r tabRule) moving() bool { return r.left.moving() || r.right.moving() }

// listGlide is the current tab's cursor bar and scroll offset, in rows.
type listGlide struct{ cursor, offset glide }

func newListGlide() listGlide {
	return listGlide{cursor: newGlide(cursorSpring, nearCell), offset: newGlide(scrollSpring, nearCell)}
}

func (l *listGlide) aim(cursor, offset int) {
	l.cursor.target, l.offset.target = float64(cursor), float64(offset)
}

func (l *listGlide) snap(cursor, offset int) {
	l.cursor.snap(float64(cursor))
	l.offset.snap(float64(offset))
}

func (l listGlide) moving() bool { return l.cursor.moving() || l.offset.moving() }

// gutter is the cursor bar's cell beside row i, with room after it: the
// whole bar on the row it rests on, or half on each of two rows it is
// passing between.
func (l listGlide) gutter(i int) string {
	base := math.Floor(l.cursor.pos)
	frac := l.cursor.pos - base

	switch row := float64(i); {
	case frac < 0.25 && row == base, frac > 0.75 && row == base+1:
		return linkText.Render("▌") + " "
	case frac >= 0.25 && frac <= 0.75 && row == base:
		return linkText.Render("▖") + " "
	case frac >= 0.25 && frac <= 0.75 && row == base+1:
		return linkText.Render("▘") + " "
	}

	return "  "
}

// barRow is the row the bar is nearest, the one drawn as selected.
func (l listGlide) barRow() int { return int(math.Round(l.cursor.pos)) }

// scrollRow is the first row drawn.
func (l listGlide) scrollRow() int { return max(0, int(math.Round(l.offset.pos))) }

// flight is the map marker, in degrees so it is the same at any map size: it
// eases from city to city, leaves a fading trail, and sends out a ring when
// it lands. Its first city it is placed on, without flying.
type flight struct {
	lat, lon glide
	trail    []trailDot
	ring     glide
	ringLeft int
	landed   bool
	// slug is the chapter it is on or flying to; "" before the first.
	slug string
}

type trailDot struct {
	lat, lon float64
	age      int
}

func newFlight() flight {
	return flight{lat: newGlide(flightSpring, nearDegree), lon: newGlide(flightSpring, nearDegree), ring: newGlide(ringSpring, nearCell)}
}

// aim flies the marker to c from wherever it is, or places it there first.
func (f *flight) aim(c *Chapter) {
	if f.slug == "" {
		f.lat.snap(c.Lat)
		f.lon.snap(c.Lon)
		f.land()
	} else {
		f.lat.target, f.lon.target = c.Lat, c.Lon
		f.landed = false
	}

	f.slug = c.Slug
}

func (f *flight) land() {
	f.landed = true
	f.ring.snap(0)
	f.ring.target = 3
	f.ringLeft = ringFrames
}

func (f *flight) step() {
	f.lat.step()
	f.lon.step()

	kept := f.trail[:0]
	for _, dot := range f.trail {
		if dot.age++; dot.age < trailFrames {
			kept = append(kept, dot)
		}
	}

	f.trail = kept

	if !f.landed {
		f.trail = append(f.trail, trailDot{lat: f.lat.pos, lon: f.lon.pos})

		if math.Hypot(f.lat.pos-f.lat.target, f.lon.pos-f.lon.target) < landingDegrees {
			f.land()
		}
	}

	if f.ringLeft > 0 {
		f.ring.step()
		f.ringLeft--
	}
}

func (f flight) moving() bool {
	return f.lat.moving() || f.lon.moving() || f.ringLeft > 0 || len(f.trail) > 0
}
