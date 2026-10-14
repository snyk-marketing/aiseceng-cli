package main

import (
	"cmp"
	"fmt"
	"image/color"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The site's palette (DESIGN.md): white carries facts, violet labels, coral
// marks what you can act on, green means today or new.
var (
	white       = lipgloss.Color("#ffffff")
	secondary   = lipgloss.Color("#b8b8b8")
	muted       = lipgloss.Color("#8f8f8f")
	hairline    = lipgloss.Color("#2e2e2e")
	violet      = lipgloss.Color("#8b5cf6")
	coral       = lipgloss.Color("#f87a98")
	green       = lipgloss.Color("#4ade80")
	cobalt      = lipgloss.Color("#1b3ff3")
	ultraviolet = lipgloss.Color("#6934da")
	sparkYellow = lipgloss.Color("#fffb2a")
	// land is the map's continents: present, but quieter than any text.
	land = lipgloss.Color("#3d3d3d")

	whiteText     = lipgloss.NewStyle().Foreground(white)
	boldText      = whiteText.Bold(true)
	secondaryText = lipgloss.NewStyle().Foreground(secondary)
	mutedText     = lipgloss.NewStyle().Foreground(muted)
	hairlineText  = lipgloss.NewStyle().Foreground(hairline)
	labelText     = lipgloss.NewStyle().Foreground(violet).Bold(true)
	linkText      = lipgloss.NewStyle().Foreground(coral)
	greenText     = lipgloss.NewStyle().Foreground(green)
	landText      = lipgloss.NewStyle().Foreground(land)
)

// cell is one table value. A cell with a link prints as an OSC 8 hyperlink,
// which most terminals make clickable and the rest print as plain text.
type cell struct {
	text  string
	color color.Color
	link  string
	bold  bool
}

const gap = "  "

// sideColumnWidth caps a job's employer and location, so the role keeps the
// room ("Carnegie Mellon University (Software Engineering Institute / CERT)").
const sideColumnWidth = 24

// renderTable prints rows aligned into columns; see tableLines.
func renderTable(w io.Writer, rows [][]cell, flex, width int) {
	for _, line := range tableLines(rows, flex, width) {
		lipgloss.Fprintln(w, line)
	}
}

// tableLines aligns rows into columns, one line per row. Every column but
// `flex` is as wide as its widest value; `flex` takes what is left of `width`
// and truncates. A width of 0 (output is not a terminal) truncates nothing.
func tableLines(rows [][]cell, flex, width int) []string {
	if len(rows) == 0 {
		return nil
	}

	widths := make([]int, len(rows[0]))

	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], ansi.StringWidth(c.text))
		}
	}

	if width > 0 {
		fixed := len(gap) * (len(widths) - 1)

		for i, cw := range widths {
			if i != flex {
				fixed += cw
			}
		}

		widths[flex] = max(min(widths[flex], width-fixed), 12)
	}

	lines := make([]string, len(rows))

	for r, row := range rows {
		parts := make([]string, len(row))

		for i, c := range row {
			text := ansi.Truncate(c.text, widths[i], "…")
			style := lipgloss.NewStyle().Foreground(c.color).Bold(c.bold).Hyperlink(c.link)

			// The last column is not padded, so a line ends where its text does.
			if i < len(row)-1 {
				text = pad(text, widths[i])
			}

			parts[i] = style.Render(text)
		}

		lines[r] = strings.Join(parts, gap)
	}

	return lines
}

// pad fills text with spaces out to width cells.
func pad(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

// sameDayAsPrevious reports whether events[i] shares the date above it, so a
// day's second meetup leaves the date blank and the list reads by day.
func sameDayAsPrevious(events []Event, i int) bool {
	return i > 0 && events[i-1].Date == events[i].Date
}

func renderEvents(w io.Writer, events []Event, width int) {
	lipgloss.Fprintln(w, labelText.Render(fmt.Sprintf("UPCOMING EVENTS  %d", len(events))))
	lipgloss.Fprintln(w)

	if len(events) == 0 {
		lipgloss.Fprintln(w, secondaryText.Render("Nothing scheduled right now."))
	}

	rows := make([][]cell, len(events))

	for i, e := range events {
		date := cell{text: e.Date, color: secondary}
		if e.Date == "Today" {
			date.color = green
		}

		if sameDayAsPrevious(events, i) {
			date.text = ""
		}

		rows[i] = []cell{
			date,
			{text: deref(e.Time), color: muted},
			{text: cmp.Or(deref(e.City), deref(e.Venue), "Online"), color: secondary},
			{text: listingLinks(e.Links), color: muted},
			{text: e.Title, color: white, link: e.URL, bold: true},
		}
	}

	renderTable(w, rows, 4, width)
	lipgloss.Fprintln(w)
	lipgloss.Fprintln(w, linkText.Hyperlink(siteURL+"/events/").Render("→ "+displayURL(siteURL+"/events/")))
}

func renderChapters(w io.Writer, chapters []Chapter, events []Event, width int) {
	lipgloss.Fprintln(w, labelText.Render(fmt.Sprintf("CHAPTERS  %d", len(chapters))))
	lipgloss.Fprintln(w)

	if len(chapters) == 0 {
		lipgloss.Fprintln(w, secondaryText.Render("No chapters match."))
	}

	rows := make([][]cell, len(chapters))

	for i, c := range chapters {
		next := cell{text: "no date yet", color: muted}
		if e := nextMeetup(events, c); e != nil {
			next = cell{text: e.Date + "  " + e.Title, color: white, link: e.URL}
		}

		rows[i] = []cell{
			{text: c.Name, color: white, link: c.PageURL(), bold: true},
			{text: deref(c.RegionName), color: muted},
			next,
		}
	}

	renderTable(w, rows, 2, width)
	lipgloss.Fprintln(w)
	lipgloss.Fprintln(w, linkText.Hyperlink(siteURL+"/chapters/").Render("→ "+displayURL(siteURL+"/chapters/")))
}

// listingLinks is an event's listings as clickable labels: "Luma Meetup".
func listingLinks(links []EventLink) string {
	labels := make([]string, len(links))
	for i, link := range links {
		labels[i] = mutedText.Hyperlink(link.URL).Render(link.Label)
	}

	return strings.Join(labels, " ")
}

// shortDate is a role's `YYYY-MM-DD` as `14 Oct`.
func shortDate(isoDate string) string {
	t, err := time.Parse(time.DateOnly, isoDate)
	if err != nil {
		return isoDate
	}

	return t.Format("02 Jan")
}

func renderJobs(w io.Writer, jobs []Job, total, width int, now time.Time) {
	lipgloss.Fprintln(w, labelText.Render(fmt.Sprintf("OPEN ROLES  %d", total)))
	lipgloss.Fprintln(w)

	if len(jobs) == 0 {
		lipgloss.Fprintln(w, secondaryText.Render("No roles match."))
	}

	today := now.Format(time.DateOnly)
	rows := make([][]cell, len(jobs))

	for i, j := range jobs {
		posted := cell{text: shortDate(j.PostedAt), color: muted}
		if j.PostedAt == today {
			posted.color = green
		}

		rows[i] = []cell{
			posted,
			{text: j.Role, color: white, link: j.ApplyURL, bold: true},
			{text: ansi.Truncate(j.Employer, sideColumnWidth, "…"), color: secondary},
			{text: ansi.Truncate(j.Location, sideColumnWidth, "…"), color: muted},
		}
	}

	// Each role's apply link prints whole on the line under it, past the date
	// column: a truncated link is a broken one, and a hyperlink on the role
	// alone is lost on a terminal without them and in a pipe.
	indent := strings.Repeat(" ", ansi.StringWidth(shortDate(today))+len(gap))

	for i, line := range tableLines(rows, 1, width) {
		lipgloss.Fprintln(w, line)
		lipgloss.Fprintln(w, indent+linkText.Hyperlink(jobs[i].ApplyURL).Render(jobs[i].ApplyURL))
	}

	if len(jobs) < total {
		lipgloss.Fprintln(w)
		lipgloss.Fprintln(w, mutedText.Render(fmt.Sprintf("%d more. Use --all to see them.", total-len(jobs))))
	}

	lipgloss.Fprintln(w)
	lipgloss.Fprintln(w, linkText.Hyperlink(siteURL+"/job-board/").Render("→ "+displayURL(siteURL+"/job-board/")))
}

// displayURL is a link as it reads on screen: aiseceng.io/events.
func displayURL(url string) string {
	for _, prefix := range []string{"https://", "http://", "www."} {
		url = strings.TrimPrefix(url, prefix)
	}

	return strings.TrimSuffix(url, "/")
}

// hostOf is a link's host: the "apply on greenhouse.io" part.
func hostOf(url string) string {
	host, _, _ := strings.Cut(displayURL(url), "/")

	return host
}
