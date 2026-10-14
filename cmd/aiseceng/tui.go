package main

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The TUI that bare `aiseceng` opens: tabs for events, jobs, chapters and
// the ways to join, each a list with the selected item's detail beside it,
// and a `/` search that works like the plain commands' search words. On the
// events and chapters tabs the detail sits under the chapter map
// (worldmap.go), with the selected city lit.
//
// Motion is one-shot by design. The banner decodes once at launch, then the
// map's land sweeps in, its chapters light up and the first rows cascade in;
// selecting another city pings it for three frames. A spring (motion.go)
// slides the tab rule to a new tab and stops once it settles. When nothing
// is moving nothing ticks, so an idle TUI costs no redraws. The search cursor
// is steady rather than blinking for the same reason.

type tab int

const (
	eventsTab tab = iota
	jobsTab
	chaptersTab
	joinTab
)

var tabNames = [...]string{"Events", "Jobs", "Chapters", "Join"}

type joinLink struct{ label, about, url string }

func joinLinks() []joinLink {
	return []joinLink{
		{"Discord", "Talk with the community", siteURL + "/discord"},
		{"Newsletter", "Read the archive and subscribe", siteURL + "/news/"},
		{"Website", "Everything else", siteURL + "/"},
	}
}

type (
	eventsMsg struct {
		events []Event
		err    error
	}
	boardMsg struct {
		board Board
		err   error
	}
	chaptersMsg struct {
		chapters []Chapter
		err      error
	}
	frameMsg  struct{}
	statusMsg string
)

const (
	frameInterval = 30 * time.Millisecond
	// splashFrames is the banner's decode, about 600ms.
	splashFrames = 20
	// introFrames is the map drawing in after it, about 650ms: the land sweeps
	// in over sweepFrames, then the chapters light up. The first rows cascade
	// in over its first cascadeFrames.
	introFrames   = 22
	sweepFrames   = 12
	cascadeFrames = 10
	// pingFrames is the ring around a newly selected city, about 100ms.
	pingFrames = 3
	// splitWidth is the narrowest terminal that gets a detail pane.
	splitWidth = 100
)

type model struct {
	ctx           context.Context
	width, height int
	tab           tab

	events     []Event
	eventsErr  error
	eventsDone bool
	board      Board
	boardErr   error
	boardDone  bool

	chapters     []Chapter
	chaptersErr  error
	chaptersDone bool

	// inputs holds the events, jobs and chapters searches, indexed by tab.
	inputs [3]textinput.Model
	cursor [len(tabNames)]int
	offset [len(tabNames)]int

	// masks keeps the map's land per size, shared by every copy of the model.
	masks map[[2]int]*landMask

	inSplash bool
	frame    int
	intro    int
	ping     int
	rule     tabRule
	ticking  bool
	rng      *rand.Rand
	status   string
	now      time.Time
}

func newModel(ctx context.Context) model {
	m := model{
		ctx:      ctx,
		masks:    map[[2]int]*landMask{},
		inSplash: true,
		rule:     newTabRule(),
		ticking:  true,
		rng:      rand.New(rand.NewPCG(1, 2)),
		now:      time.Now(),
	}

	placeholders := [3]string{"events, cities, venues, dates", "roles, companies, places, levels, focus areas", "chapters, cities, regions"}

	for i := range m.inputs {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholders[i]

		styles := textinput.DefaultDarkStyles()
		styles.Focused.Text = whiteText
		styles.Blurred.Text = whiteText
		styles.Focused.Placeholder = mutedText
		styles.Blurred.Placeholder = mutedText
		styles.Cursor.Color = coral
		styles.Cursor.Blink = false
		input.SetStyles(styles)

		m.inputs[i] = input
	}

	return m
}

func runTUI(ctx context.Context) error {
	_, err := tea.NewProgram(newModel(ctx), tea.WithContext(ctx)).Run()

	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadEvents(), m.loadBoard(), m.loadChapters(), nextFrame())
}

func (m model) loadEvents() tea.Cmd {
	return func() tea.Msg {
		events, err := fetchEvents(m.ctx)

		return eventsMsg{events, err}
	}
}

func (m model) loadBoard() tea.Cmd {
	return func() tea.Msg {
		board, err := fetchBoard(m.ctx)

		return boardMsg{board, err}
	}
}

func (m model) loadChapters() tea.Cmd {
	return func() tea.Msg {
		chapters, err := fetchChapters(m.ctx)

		return chaptersMsg{chapters, err}
	}
}

func nextFrame() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

func open(url string) tea.Cmd {
	return func() tea.Msg {
		if err := openURL(url); err != nil {
			return statusMsg("No browser to open, so here is the link: " + url)
		}

		return nil
	}
}

// ---------- update ----------

func (m model) animating() bool {
	return (m.inSplash && m.frame < splashFrames) || (!m.inSplash && (m.intro < introFrames || m.ping > 0 || m.moving()))
}

func (m model) moving() bool {
	return m.rule.moving()
}

// stepMotion moves every spring one frame. The rule's targets follow the tab
// bar, whose labels widen as counts arrive.
func (m *model) stepMotion() {
	if m.rule.moving() {
		span := tabSpans(m.tabLabels())[m.tab]
		m.rule.left.target, m.rule.right.target = float64(span[0]), float64(span[1])
	}

	m.rule.left.step()
	m.rule.right.step()
}

// leaveSplash ends the splash once the banner has decoded and every read has
// answered, either way.
func (m *model) leaveSplash() {
	if m.inSplash && m.frame >= splashFrames && m.eventsDone && m.boardDone && m.chaptersDone {
		m.inSplash = false
		m.intro = 0
	}
}

// resumeFrames restarts the frame clock when an animation is due and the
// clock has stopped, so there is only ever one chain of ticks.
func (m *model) resumeFrames() tea.Cmd {
	if m.animating() && !m.ticking {
		m.ticking = true

		return nextFrame()
	}

	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.inputs {
			m.inputs[i].SetWidth(max(10, m.width-m.logoInset()-24))
		}

		m.clampScroll()

		return m, nil

	case eventsMsg:
		m.events, m.eventsErr, m.eventsDone = msg.events, msg.err, true
		m.leaveSplash()

		return m, m.resumeFrames()

	case boardMsg:
		m.board, m.boardErr, m.boardDone = msg.board, msg.err, true
		m.leaveSplash()

		return m, m.resumeFrames()

	case chaptersMsg:
		m.chapters, m.chaptersErr, m.chaptersDone = msg.chapters, msg.err, true
		m.leaveSplash()

		return m, m.resumeFrames()

	case frameMsg:
		switch {
		case m.inSplash:
			m.frame++
			m.leaveSplash()
		case m.intro < introFrames:
			m.intro++

			// The intro ends by pinging the city the cursor starts on.
			if m.intro == introFrames && m.highlight() != nil {
				m.ping = pingFrames
			}

			m.stepMotion()
		default:
			m.ping = max(0, m.ping-1)
			m.stepMotion()
		}

		if m.animating() {
			return m, nextFrame()
		}

		m.ticking = false

		return m, nil

	case statusMsg:
		m.status = string(msg)

		return m, nil

	case tea.KeyPressMsg:
		m.status = ""
		before, beforeTab := m.highlight(), m.tab

		next, cmd := m.handleKey(msg)
		m = next.(model)

		if m.tab != beforeTab {
			m.rule.slide(tabSpans(m.tabLabels()), beforeTab, m.tab)
		}

		// Moving onto another city pings it, once the map has drawn in.
		if after := m.highlight(); after != nil && m.intro >= introFrames && (before == nil || before.Slug != after.Slug) {
			m.ping = pingFrames
		}

		return m, tea.Batch(cmd, m.resumeFrames())
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.inSplash {
		if key == "q" || key == "esc" {
			return m, tea.Quit
		}

		// Any other key skips the decode, once there is something to show.
		m.frame = splashFrames
		m.leaveSplash()

		return m, m.resumeFrames()
	}

	if input := m.input(); input != nil && input.Focused() {
		switch key {
		case "esc":
			input.SetValue("")
			input.Blur()
			m.resetCursor()
		case "enter":
			input.Blur()
		case "up", "ctrl+p":
			m.move(-1)
		case "down", "ctrl+n":
			m.move(1)
		case "tab", "shift+tab":
			input.Blur()
			m.switchTab(key)
		default:
			before := input.Value()

			var cmd tea.Cmd

			*input, cmd = input.Update(msg)
			if input.Value() != before {
				m.resetCursor()
			}

			return m, cmd
		}

		return m, nil
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "tab", "shift+tab", "right", "left":
		m.switchTab(key)
	case "1", "2", "3", "4":
		m.tab = tab(key[0] - '1')
	case "/":
		if input := m.input(); input != nil {
			return m, input.Focus()
		}
	case "esc":
		if input := m.input(); input != nil {
			input.SetValue("")
			m.resetCursor()
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.bodyHeight())
	case "pgdown", "space":
		m.move(m.bodyHeight())
	case "home", "g":
		m.move(-m.count())
	case "end", "G":
		m.move(m.count())
	case "enter":
		if url := m.selectedURL(); url != "" {
			return m, open(url)
		}
	case "c":
		if url := m.selectedURL(); url != "" {
			m.status = "Copied " + displayURL(url)

			return m, tea.SetClipboard(url)
		}
	case "r":
		return m.retry()
	case "l", "m":
		if url := m.selectedListing(key); url != "" {
			return m, open(url)
		}
	}

	return m, nil
}

// selectedListing is the page `key` opens for the selection: an event's Luma
// or Meetup listing (see linkKey), or a chapter's Meetup group on m. It is ""
// when the selection has no such page.
func (m model) selectedListing(key string) string {
	i := m.cursor[m.tab]

	switch m.tab {
	case eventsTab:
		if events := m.visibleEvents(); i < len(events) {
			for _, link := range events[i].Links {
				if linkKey(link) == key {
					return link.URL
				}
			}
		}
	case chaptersTab:
		if chapters := m.visibleChapters(); i < len(chapters) && key == "m" {
			return deref(chapters[i].Meetup)
		}
	}

	return ""
}

func (m *model) switchTab(key string) {
	step := 1
	if key == "shift+tab" || key == "left" {
		step = len(tabNames) - 1
	}

	m.tab = (m.tab + tab(step)) % tab(len(tabNames))
}

// retry rereads the current tab's feed after a failed read.
func (m model) retry() (tea.Model, tea.Cmd) {
	switch {
	case m.tab == eventsTab && m.eventsErr != nil:
		m.eventsErr, m.eventsDone = nil, false

		return m, m.loadEvents()
	case m.tab == jobsTab && m.boardErr != nil:
		m.boardErr, m.boardDone = nil, false

		return m, m.loadBoard()
	case m.tab == chaptersTab && m.chaptersErr != nil:
		m.chaptersErr, m.chaptersDone = nil, false

		return m, m.loadChapters()
	}

	return m, nil
}

// input is the current tab's search, or nil on a tab without one.
func (m *model) input() *textinput.Model {
	if m.tab == joinTab {
		return nil
	}

	return &m.inputs[m.tab]
}

func (m model) visibleEvents() []Event { return searchEvents(m.events, m.inputs[eventsTab].Value()) }

func (m model) visibleJobs() []Job { return searchJobs(m.board, m.inputs[jobsTab].Value()) }

func (m model) visibleChapters() []Chapter {
	return searchChapters(byNextMeetup(m.chapters, m.events), m.inputs[chaptersTab].Value())
}

// chapter is the chapter with this slug, or nil.
func (m model) chapter(slug *string) *Chapter {
	for i := range m.chapters {
		if slug != nil && m.chapters[i].Slug == *slug {
			return &m.chapters[i]
		}
	}

	return nil
}

// highlight is the chapter the map lights for the selection: the selected
// event's, or the selected chapter. nil on other tabs.
func (m model) highlight() *Chapter {
	i := m.cursor[m.tab]

	switch m.tab {
	case eventsTab:
		if events := m.visibleEvents(); i < len(events) {
			return m.chapter(events[i].Chapter)
		}
	case chaptersTab:
		if chapters := m.visibleChapters(); i < len(chapters) {
			return &chapters[i]
		}
	}

	return nil
}

func (m model) count() int {
	switch m.tab {
	case eventsTab:
		return len(m.visibleEvents())
	case jobsTab:
		return len(m.visibleJobs())
	case chaptersTab:
		return len(m.visibleChapters())
	default:
		return len(joinLinks())
	}
}

func (m model) selectedURL() string {
	i := m.cursor[m.tab]

	switch m.tab {
	case eventsTab:
		if events := m.visibleEvents(); i < len(events) {
			return events[i].URL
		}
	case jobsTab:
		if jobs := m.visibleJobs(); i < len(jobs) {
			return jobs[i].ApplyURL
		}
	case chaptersTab:
		if chapters := m.visibleChapters(); i < len(chapters) {
			return chapters[i].PageURL()
		}
	default:
		return joinLinks()[i].url
	}

	return ""
}

func (m *model) resetCursor() {
	m.cursor[m.tab], m.offset[m.tab] = 0, 0
}

func (m *model) move(delta int) {
	m.cursor[m.tab] = max(0, min(m.cursor[m.tab]+delta, m.count()-1))
	m.clampScroll()
}

// clampScroll keeps the cursor inside the visible window of rows.
func (m *model) clampScroll() {
	body := m.bodyHeight()
	cursor, offset := m.cursor[m.tab], m.offset[m.tab]

	if cursor < offset {
		offset = cursor
	}

	if cursor >= offset+body {
		offset = cursor - body + 1
	}

	m.offset[m.tab] = max(0, offset)
}

// ---------- view ----------

// bodyHeight is the rows between the search line and the key bar.
func (m model) bodyHeight() int {
	return max(1, m.height-6)
}

func (m model) View() tea.View {
	var content string

	switch {
	case m.width == 0:
	case m.inSplash:
		content = m.splashView()
	default:
		content = m.mainView()
	}

	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "aiseceng"

	return view
}

func (m model) splashView() string {
	reveal := min(1, float64(m.frame)/splashFrames)

	lines := []string{
		renderBanner(1-(1-reveal)*(1-reveal)*(1-reveal), m.rng),
		"",
		mutedText.Render("AI Security Engineers"),
	}

	if m.frame >= splashFrames {
		lines = append(lines, "", mutedText.Render("Loading events, roles and chapters"))
	}

	block := lipgloss.JoinVertical(lipgloss.Left, lines...)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

func (m model) mainView() string {
	lines := m.header()

	body := m.bodyHeight()

	listWidth, detailWidth := m.width, 0
	if m.width >= splitWidth {
		listWidth = min(64, m.width*48/100)
		detailWidth = m.width - listWidth - 3
	}

	rows, detail := m.panes(listWidth, max(0, detailWidth-1), body)

	// The first rows cascade in after the splash.
	if m.intro < cascadeFrames {
		rows = rows[:min(len(rows), (m.intro+1)*body/cascadeFrames)]
	}

	list := block(rows, listWidth, body)

	if detailWidth > 0 {
		right := block(detail, detailWidth-1, body)

		for i := range list {
			list[i] += " " + hairlineText.Render("│") + " " + right[i]
		}
	}

	lines = append(lines, list...)
	lines = append(lines, hairlineText.Render(strings.Repeat("─", m.width)), m.keyBar())

	return strings.Join(lines, "\n")
}

// logoInset is how many columns the logo takes beside the header, on terminals
// wide enough to spare them.
func (m model) logoInset() int {
	if m.width < splitWidth {
		return 0
	}

	return logoWidth + 2
}

// header is the tab bar, the search line and a blank line, with the logo
// beside them when there is room.
func (m model) header() []string {
	inset := m.logoInset()
	lines := append(m.tabBar(m.width-inset), m.searchLine(m.width-inset), "")

	if inset > 0 {
		for i := range lines {
			lines[i] = " " + logoRows[i] + " " + lines[i]
		}
	}

	return lines
}

// tabLabels is each tab's name, with its count once its feed has loaded.
func (m model) tabLabels() []string {
	labels := slices.Clone(tabNames[:])

	if m.eventsDone && m.eventsErr == nil {
		labels[eventsTab] += fmt.Sprintf(" %d", len(m.events))
	}

	if m.boardDone && m.boardErr == nil {
		labels[jobsTab] += fmt.Sprintf(" %d", len(m.board.Jobs))
	}

	if m.chaptersDone && m.chaptersErr == nil {
		labels[chaptersTab] += fmt.Sprintf(" %d", len(m.chapters))
	}

	return labels
}

// tabLead is the wordmark before the tabs.
var tabLead = " " + boldText.Render("aiseceng") + "    "

// tabSpans is the cells each label covers in the tab bar, [start, end).
func tabSpans(labels []string) [][2]int {
	spans := make([][2]int, len(labels))
	x := ansi.StringWidth(tabLead)

	for i, label := range labels {
		spans[i] = [2]int{x, x + ansi.StringWidth(label)}
		x = spans[i][1] + 3
	}

	return spans
}

// tabBar is the wordmark and tabs over a hairline, width cells wide, with a
// coral rule under the active tab, or sliding to it.
func (m model) tabBar(width int) []string {
	labels := m.tabLabels()
	spans := tabSpans(labels)
	top := tabLead

	for i, label := range labels {
		if tab(i) == m.tab {
			top += boldText.Render(label) + "   "
		} else {
			top += mutedText.Render(label) + "   "
		}
	}

	left, right := float64(spans[m.tab][0]), float64(spans[m.tab][1])
	if m.rule.moving() {
		left, right = m.rule.left.pos, m.rule.right.pos
	}

	// Each cell is the rule, half of it at an edge (╺ ╸), the hairline under
	// a label and past the last, or a gap.
	hairlineFrom := spans[len(spans)-1][1] + 3
	under := make([]styledRune, max(0, width))

	for x := range under {
		cover := min(float64(x+1), right) - max(float64(x), left)
		hairline := x >= hairlineFrom || slices.ContainsFunc(spans, func(s [2]int) bool { return x >= s[0] && x < s[1] })

		switch {
		case cover >= 0.7:
			under[x] = styledRune{'━', &linkText}
		case cover >= 0.3 && left > float64(x):
			under[x] = styledRune{'╺', &linkText}
		case cover >= 0.3:
			under[x] = styledRune{'╸', &linkText}
		case hairline:
			under[x] = styledRune{'─', &hairlineText}
		default:
			under[x] = styledRune{' ', nil}
		}
	}

	return []string{fit(top, width), renderRunes(under)}
}

// styledRune is one cell of a line drawn cell by cell; a nil style is plain.
type styledRune struct {
	r     rune
	style *lipgloss.Style
}

// renderRunes draws a line of cells, styling each run of cells that share a
// style once.
func renderRunes(cells []styledRune) string {
	var b, run strings.Builder

	for i, c := range cells {
		run.WriteRune(c.r)

		if i < len(cells)-1 && cells[i+1].style == c.style {
			continue
		}

		if c.style == nil {
			b.WriteString(run.String())
		} else {
			b.WriteString(c.style.Render(run.String()))
		}

		run.Reset()
	}

	return b.String()
}

func (m model) searchLine(width int) string {
	input := m.input()
	if input == nil {
		return ""
	}

	// The selection's position among the rows the search leaves visible.
	total, position := m.count(), 0
	if total > 0 {
		position = m.cursor[m.tab] + 1
	}

	count := mutedText.Render(fmt.Sprintf("%d of %d ", position, total))
	left := " " + linkText.Render("/") + " " + input.View()

	return fit(left, width-ansi.StringWidth(count)) + count
}

func (m model) keyBar() string {
	if m.status != "" {
		return " " + whiteText.Render(m.status)
	}

	var pairs []string

	switch {
	case m.input() != nil && m.input().Focused():
		pairs = []string{"↑↓", "move", "enter", "done", "esc", "clear"}
	case m.tab == eventsTab:
		pairs = []string{"↑↓", "move", "/", "search", "enter", "rsvp", "c", "copy link", "tab", "jobs", "q", "quit"}
	case m.tab == jobsTab:
		pairs = []string{"↑↓", "move", "/", "search", "enter", "apply", "c", "copy link", "tab", "chapters", "q", "quit"}
	case m.tab == chaptersTab:
		pairs = []string{"↑↓", "move", "/", "search", "enter", "chapter page", "c", "copy link", "tab", "join", "q", "quit"}
	default:
		pairs = []string{"↑↓", "move", "enter", "open", "c", "copy link", "tab", "events", "q", "quit"}
	}

	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		parts = append(parts, whiteText.Render(pairs[i])+" "+mutedText.Render(pairs[i+1]))
	}

	return " " + strings.Join(parts, "   ")
}

// panes is the current tab's visible list rows and its detail.
func (m model) panes(listWidth, detailWidth, body int) (rows, detail []string) {
	switch m.tab {
	case eventsTab:
		if notice := feedNotice(m.eventsDone, m.eventsErr, "events"); notice != nil {
			return notice, nil
		}

		if len(m.events) == 0 {
			return []string{" " + secondaryText.Render("Nothing scheduled right now.")}, nil
		}

		events := m.visibleEvents()
		if len(events) == 0 {
			return emptyNotice("events"), nil
		}

		for i := range events {
			rows = append(rows, eventRow(events, i, i == m.cursor[eventsTab], listWidth))
		}

		lines, actions := eventDetail(events[m.cursor[eventsTab]], detailWidth)
		detail = m.mapDetail(lines, actions, detailWidth, body)
	case jobsTab:
		if notice := feedNotice(m.boardDone, m.boardErr, "roles"); notice != nil {
			return notice, nil
		}

		jobs := m.visibleJobs()
		if len(jobs) == 0 {
			return emptyNotice("roles"), nil
		}

		for i, j := range jobs {
			rows = append(rows, jobRow(j, i == m.cursor[jobsTab], listWidth, m.now))
		}

		detail = jobDetail(m.board, jobs[m.cursor[jobsTab]], detailWidth, body)
	case chaptersTab:
		if notice := feedNotice(m.chaptersDone, m.chaptersErr, "chapters"); notice != nil {
			return notice, nil
		}

		chapters := m.visibleChapters()
		if len(chapters) == 0 {
			return emptyNotice("chapters"), nil
		}

		for i, c := range chapters {
			rows = append(rows, chapterRow(c, nextMeetup(m.events, c), i == m.cursor[chaptersTab], listWidth))
		}

		c := chapters[m.cursor[chaptersTab]]
		lines, actions := chapterDetail(c, nextMeetup(m.events, c), detailWidth)
		detail = m.mapDetail(lines, actions, detailWidth, body)
	default:
		links := joinLinks()

		for i, link := range links {
			rows = append(rows, joinRow(link, i == m.cursor[joinTab], listWidth))
		}

		detail = joinDetail(links[m.cursor[joinTab]], detailWidth, body)
	}

	offset := m.offset[m.tab]

	return rows[min(offset, len(rows)):min(offset+body, len(rows))], detail
}

func feedNotice(done bool, err error, what string) []string {
	switch {
	case !done:
		return []string{" " + mutedText.Render("Loading "+what)}
	case err != nil:
		return append(
			wrapStyled(" Could not load "+what+". "+err.Error(), 60, secondaryText),
			"",
			" "+linkText.Render("r")+mutedText.Render("  try again"),
		)
	}

	return nil
}

func emptyNotice(what string) []string {
	return []string{" " + secondaryText.Render("No "+what+" match."), " " + mutedText.Render("esc clears the search")}
}

// selectable prefixes a row with the cursor's coral bar, or room for it.
func selectable(selected bool) string {
	if selected {
		return linkText.Render("▌ ")
	}

	return "  "
}

func jobRow(j Job, selected bool, width int, now time.Time) string {
	const dateWidth, employerWidth = 6, 14

	roleWidth := max(10, width-2-dateWidth-2-2-employerWidth)
	date, role, employer := mutedText, secondaryText, mutedText

	if selected {
		date, role, employer = whiteText, boldText, whiteText
	}

	if j.PostedAt == now.Format(time.DateOnly) {
		date = greenText
	}

	return selectable(selected) +
		date.Render(fit(shortDate(j.PostedAt), dateWidth)) + gap +
		role.Render(fit(j.Role, roleWidth)) + gap +
		employer.Render(fit(j.Employer, employerWidth))
}

func eventRow(events []Event, i int, selected bool, width int) string {
	const dateWidth, timeWidth, cityWidth = 10, 5, 13

	e := events[i]
	titleWidth := max(10, width-2-dateWidth-2-timeWidth-2-cityWidth-2)
	date, meta, title := secondaryText, mutedText, secondaryText

	if selected {
		date, meta, title = whiteText, whiteText, boldText
	}

	if e.Date == "Today" {
		date = greenText
	}

	day := e.Date
	if sameDayAsPrevious(events, i) {
		day = ""
	}

	return selectable(selected) +
		date.Render(fit(day, dateWidth)) + gap +
		meta.Render(fit(strings.TrimSuffix(deref(e.Time), " local"), timeWidth)) + gap +
		meta.Render(fit(cmp.Or(deref(e.City), "Online"), cityWidth)) + gap +
		title.Render(fit(e.Title, titleWidth))
}

func joinRow(link joinLink, selected bool, width int) string {
	label := secondaryText
	if selected {
		label = boldText
	}

	return selectable(selected) + label.Render(fit(link.label, 14)) + mutedText.Render(fit(displayURL(link.url), width-18))
}

// field is one `LABEL  value` line of a detail pane.
func field(label, value string) string {
	return labelText.Render(pad(strings.ToUpper(label), 11)) + whiteText.Render(value)
}

// withActions pins an action line to the bottom of a detail pane, cutting the
// content above it to fit.
func withActions(content []string, actions string, body int) []string {
	room := max(0, body-2)
	if len(content) > room {
		content = append(content[:max(0, room-1)], mutedText.Render("…"))
	}

	for len(content) < room {
		content = append(content, "")
	}

	return append(content, "", actions)
}

func action(key, label string) string {
	return linkText.Render(key) + "  " + mutedText.Render(label)
}

func jobDetail(board Board, j Job, width, body int) []string {
	if width <= 0 {
		return nil
	}

	lines := wrapStyled(j.Role, width, boldText)
	lines = append(lines,
		secondaryText.Render(j.Employer)+mutedText.Render("  ·  "+j.Location),
		"",
		field("Seniority", board.Label(j.Seniority)),
		field("Region", strings.Join(board.Labels(j.Regions), ", ")),
		field("Focus", strings.Join(board.Labels(j.TechnicalDomains), ", ")),
		field("Employer", board.Label(j.EmployerType)),
		field("Posted", shortDate(j.PostedAt)),
		"",
	)
	lines = append(lines, wrapStyled(j.Description, width, secondaryText)...)

	return withActions(lines, action("enter", "apply on "+hostOf(j.ApplyURL))+"   "+action("c", "copy link"), body)
}

// mapDetail is a detail pane under the chapter map, with the selection's
// city lit. The map draws in during the intro and the text lands after it; a
// pane too short for both drops the map.
func (m model) mapDetail(lines []string, actions string, width, body int) []string {
	if width <= 0 {
		return nil
	}

	if m.intro < introFrames {
		lines, actions = nil, ""
	}

	rows := min(16, width*14/58, body-16)
	if rows < 6 {
		return withActions(lines, actions, body)
	}

	size := [2]int{width, rows}
	if m.masks[size] == nil {
		m.masks[size] = newLandMask(width, rows)
	}

	frame := mapFrame{reveal: 1, lit: len(m.chapters), highlight: m.highlight()}

	switch {
	case m.intro < sweepFrames:
		t := float64(m.intro) / sweepFrames
		frame = mapFrame{reveal: 1 - (1-t)*(1-t)*(1-t)}
	case m.intro < introFrames:
		frame = mapFrame{reveal: 1, lit: len(m.chapters) * (m.intro - sweepFrames) / (introFrames - sweepFrames)}
	case m.ping > 0:
		frame.ring = pingFrames - m.ping + 1
	}

	content := append(m.masks[size].render(m.chapters, frame), "")

	return withActions(append(content, lines...), actions, body)
}

func eventDetail(e Event, width int) (lines []string, actions string) {
	if width <= 0 {
		return nil, ""
	}

	lines = wrapStyled(e.Title, width, boldText)
	lines = append(lines, "", field("When", strings.TrimSpace(e.Date+"  "+deref(e.Time))), field("Chapter", cmp.Or(deref(e.City), "Online")))

	if e.Venue != nil {
		lines = append(lines, field("Venue", *e.Venue))
	}

	lines = append(lines, "")

	keys := []string{action("enter", "rsvp on "+hostOf(e.URL))}

	for i, link := range e.Links {
		lines = append(lines, field(link.Label, displayURL(link.URL)))

		if i == 0 {
			keys[0] = action("enter", "rsvp on "+link.Label)
		} else if key := linkKey(link); key != "" {
			keys = append(keys, action(key, link.Label))
		}
	}

	return lines, strings.Join(append(keys, action("c", "copy link")), "   ")
}

func chapterRow(c Chapter, next *Event, selected bool, width int) string {
	name := secondaryText
	if selected {
		name = boldText
	}

	when := mutedText.Render("no date yet")
	if next != nil {
		when = greenText.Render("●") + " " + whiteText.Render(next.Date)
	}

	dot := lipgloss.NewStyle().Foreground(cmp.Or(regionColors[deref(c.Region)], white)).Render("●")

	return selectable(selected) + dot + " " + name.Render(fit(c.Name, max(10, width-20))) + gap + when
}

func chapterDetail(c Chapter, next *Event, width int) (lines []string, actions string) {
	if width <= 0 {
		return nil, ""
	}

	lines = []string{boldText.Render(c.Name), mutedText.Render(deref(c.RegionName)), ""}

	if next != nil {
		lines = append(lines, field("Next", strings.TrimSpace(next.Date+"  "+deref(next.Time))))

		// The title hangs under the date, past the label column.
		for _, line := range wrapStyled(next.Title, max(1, width-11), whiteText) {
			lines = append(lines, strings.Repeat(" ", 11)+line)
		}
	}

	keys := []string{action("enter", "chapter page")}

	if c.Meetup != nil {
		lines = append(lines, field("Meetup", displayURL(*c.Meetup)))
		keys = append(keys, action("m", "Meetup group"))
	}

	lines = append(lines, field("Page", displayURL(c.PageURL())))

	return lines, strings.Join(append(keys, action("c", "copy link")), "   ")
}

func joinDetail(link joinLink, width, body int) []string {
	if width <= 0 {
		return nil
	}

	lines := []string{boldText.Render(link.label), secondaryText.Render(link.about), "", linkText.Render(displayURL(link.url))}

	return withActions(lines, action("enter", "open in your browser"), body)
}

// ---------- layout helpers ----------

// fit truncates or pads text to exactly width cells.
func fit(text string, width int) string {
	return pad(ansi.Truncate(text, max(0, width), "…"), width)
}

// block is exactly height lines, each exactly width cells.
func block(lines []string, width, height int) []string {
	out := make([]string, height)

	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], width)
		} else {
			out[i] = strings.Repeat(" ", max(0, width))
		}
	}

	return out
}

func wrapStyled(text string, width int, style lipgloss.Style) []string {
	lines := strings.Split(lipgloss.Wrap(text, max(1, width), " "), "\n")

	for i, line := range lines {
		lines[i] = style.Render(line)
	}

	return lines
}
