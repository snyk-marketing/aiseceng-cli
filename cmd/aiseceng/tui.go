package main

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The TUI that bare `aiseceng` opens: tabs for events, jobs, chapters and
// the ways to join, each a list with the selected item's detail beside it,
// and a `/` search that works like the plain commands' search words. On the
// events and chapters tabs the detail sits under the chapter map
// (worldmap.go), with the selected city lit. A detail longer than its pane
// scrolls, and `?` swaps the detail for a reference of every key (keys.go).
//
// Motion is one-shot by design. The banner decodes once at launch, then the
// map's land sweeps in, its chapters light up and the first rows cascade in;
// selecting another city pings it for three frames. After that nothing
// ticks, so an idle TUI costs no redraws. The search cursor is steady rather
// than blinking for the same reason.

type tab int

const (
	eventsTab tab = iota
	jobsTab
	chaptersTab
	joinTab
)

var tabNames = [...]string{"Events", "Jobs", "Chapters", "Join"}

// openVerbs is what enter does on each tab, for the key bar.
var openVerbs = [...]string{"rsvp", "apply", "chapter page", "open"}

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

	// detail scrolls the selection's detail, kept in step with the selection
	// by syncDetail; detailFor is the selection it holds.
	detail    viewport.Model
	detailFor selection
	showKeys  bool
	bar       help.Model

	inSplash bool
	frame    int
	intro    int
	ping     int
	ticking  bool
	rng      *rand.Rand
	status   string
	now      time.Time
}

func newModel(ctx context.Context) model {
	m := model{
		ctx:      ctx,
		masks:    map[[2]int]*landMask{},
		detail:   viewport.New(),
		bar:      newKeyBar(),
		inSplash: true,
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
	return (m.inSplash && m.frame < splashFrames) || (!m.inSplash && (m.intro < introFrames || m.ping > 0))
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
	m, cmd := m.update(msg)
	m.syncDetail()

	return m, cmd
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.inputs {
			m.inputs[i].SetWidth(max(10, m.width-24))
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
		default:
			m.ping = max(0, m.ping-1)
		}

		if m.animating() {
			return m, nextFrame()
		}

		m.ticking = false

		return m, nil

	case statusMsg:
		m.status = string(msg)

		return m, nil

	case tea.MouseWheelMsg:
		if m.inSplash {
			return m, nil
		}

		// The wheel scrolls the detail under the pointer, or moves the cursor
		// over the list.
		listWidth, _ := m.paneWidths()
		before := m.highlight()

		switch {
		case msg.X > listWidth && !m.showKeys:
			m.detail, _ = m.detail.Update(msg)
		case msg.Button == tea.MouseWheelUp:
			m.move(-1)
		case msg.Button == tea.MouseWheelDown:
			m.move(1)
		}

		return m, m.pingMove(before)

	case tea.KeyPressMsg:
		m.status = ""
		before := m.highlight()

		next, cmd := m.handleKey(msg)
		m = next.(model)

		return m, tea.Batch(cmd, m.pingMove(before))
	}

	return m, nil
}

// pingMove pings the city the cursor moved onto from `before`, once the map
// has drawn in.
func (m *model) pingMove(before *Chapter) tea.Cmd {
	if after := m.highlight(); after != nil && m.intro >= introFrames && (before == nil || before.Slug != after.Slug) {
		m.ping = pingFrames

		return m.resumeFrames()
	}

	return nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.ForceQuit) {
		return m, tea.Quit
	}

	if m.inSplash {
		if key.Matches(msg, keys.Quit, keys.Clear) {
			return m, tea.Quit
		}

		// Any other key skips the decode, once there is something to show.
		m.frame = splashFrames
		m.leaveSplash()

		return m, m.resumeFrames()
	}

	if input := m.input(); input != nil && input.Focused() {
		switch {
		case key.Matches(msg, typingKeys.Clear):
			input.SetValue("")
			input.Blur()
			m.resetCursor()
		case key.Matches(msg, typingKeys.Done):
			input.Blur()
		case key.Matches(msg, typingKeys.Up):
			m.move(-1)
		case key.Matches(msg, typingKeys.Down):
			m.move(1)
		case key.Matches(msg, typingKeys.NextTab):
			input.Blur()
			m.switchTab(1)
		case key.Matches(msg, typingKeys.PrevTab):
			input.Blur()
			m.switchTab(-1)
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

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.showKeys = !m.showKeys
	case key.Matches(msg, keys.NextTab):
		m.switchTab(1)
	case key.Matches(msg, keys.PrevTab):
		m.switchTab(-1)
	case key.Matches(msg, keys.JumpTab):
		m.tab = tab(msg.String()[0] - '1')
	case key.Matches(msg, keys.Search):
		if input := m.input(); input != nil {
			return m, input.Focus()
		}
	case key.Matches(msg, keys.Clear):
		// esc closes the key reference first, then clears the search.
		if m.showKeys {
			m.showKeys = false
		} else if input := m.input(); input != nil {
			input.SetValue("")
			m.resetCursor()
		}
	case key.Matches(msg, keys.Up):
		m.move(-1)
	case key.Matches(msg, keys.Down):
		m.move(1)
	case key.Matches(msg, keys.PageUp):
		m.move(-m.bodyHeight())
	case key.Matches(msg, keys.PageDown):
		m.move(m.bodyHeight())
	case key.Matches(msg, keys.Top):
		m.move(-m.count())
	case key.Matches(msg, keys.Bottom):
		m.move(m.count())
	case key.Matches(msg, keys.ScrollUp):
		m.detail.ScrollUp(1)
	case key.Matches(msg, keys.ScrollDown):
		m.detail.ScrollDown(1)
	case key.Matches(msg, keys.Open):
		if url := m.selectedURL(); url != "" {
			return m, open(url)
		}
	case key.Matches(msg, keys.Copy):
		if url := m.selectedURL(); url != "" {
			m.status = "Copied " + displayURL(url)

			return m, tea.SetClipboard(url)
		}
	case key.Matches(msg, keys.Retry):
		return m.retry()
	case key.Matches(msg, keys.Luma, keys.Meetup):
		if url := m.selectedListing(msg); url != "" {
			return m, open(url)
		}
	}

	return m, nil
}

// selectedListing is the page msg opens for the selection: an event's Luma
// or Meetup listing (see listingKey), or a chapter's Meetup group on m. It is ""
// when the selection has no such page.
func (m model) selectedListing(msg tea.KeyPressMsg) string {
	i := m.cursor[m.tab]

	switch m.tab {
	case eventsTab:
		if events := m.visibleEvents(); i < len(events) {
			for _, link := range events[i].Links {
				if b, ok := listingKey(link); ok && key.Matches(msg, b) {
					return link.URL
				}
			}
		}
	case chaptersTab:
		if chapters := m.visibleChapters(); i < len(chapters) && key.Matches(msg, keys.Meetup) {
			return deref(chapters[i].Meetup)
		}
	}

	return ""
}

// switchTab moves step tabs along, wrapping around.
func (m *model) switchTab(step int) {
	m.tab = (m.tab + tab(step+len(tabNames))) % tab(len(tabNames))
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

// selection is what the detail pane shows: the tab, the cursor and the
// selected item's link.
type selection struct {
	tab    tab
	cursor int
	url    string
}

func (m model) selection() selection {
	return selection{m.tab, m.cursor[m.tab], m.selectedURL()}
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
	view.MouseMode = tea.MouseModeCellMotion
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
	listWidth, detailWidth := m.paneWidths()
	rows := m.listRows(listWidth, body)

	// The first rows cascade in after the splash.
	if m.intro < cascadeFrames {
		rows = rows[:min(len(rows), (m.intro+1)*body/cascadeFrames)]
	}

	// Without a detail pane, the key reference takes the list's place.
	if m.showKeys && detailWidth == 0 {
		rows = referenceLines()
	}

	list := block(rows, listWidth, body)

	if detailWidth > 0 {
		right := block(m.detailView(detailWidth, body), detailWidth, body)

		for i := range list {
			list[i] += " " + hairlineText.Render("│") + " " + right[i]
		}
	}

	lines = append(lines, list...)
	lines = append(lines, hairlineText.Render(strings.Repeat("─", m.width)), m.keyBar())

	return strings.Join(lines, "\n")
}

// header is the tab bar, the search line and a blank line.
func (m model) header() []string {
	return append(m.tabBar(), m.searchLine(), "")
}

// paneWidths is the list's width and the detail pane's, which is 0 in a
// terminal too narrow for one.
func (m model) paneWidths() (list, detail int) {
	if m.width < splitWidth {
		return m.width, 0
	}

	list = min(64, m.width*48/100)

	return list, m.width - list - 4
}

// tabBar is the wordmark and tabs, with a coral rule under the active tab.
func (m model) tabBar() []string {
	top := " " + boldText.Render("aiseceng") + "    "
	under := strings.Repeat(" ", ansi.StringWidth(top))

	var counts [len(tabNames)]string
	if m.eventsDone && m.eventsErr == nil {
		counts[eventsTab] = fmt.Sprintf(" %d", len(m.events))
	}

	if m.boardDone && m.boardErr == nil {
		counts[jobsTab] = fmt.Sprintf(" %d", len(m.board.Jobs))
	}

	if m.chaptersDone && m.chaptersErr == nil {
		counts[chaptersTab] = fmt.Sprintf(" %d", len(m.chapters))
	}

	for i, name := range tabNames {
		label := name + counts[i]
		width := ansi.StringWidth(label)

		if tab(i) == m.tab {
			top += boldText.Render(label)
			under += linkText.Render(strings.Repeat("━", width))
		} else {
			top += mutedText.Render(label)
			under += hairlineText.Render(strings.Repeat("─", width))
		}

		top += "   "
		under += "   "
	}

	under += hairlineText.Render(strings.Repeat("─", max(0, m.width-ansi.StringWidth(under))))

	return []string{fit(top, m.width), under}
}

func (m model) searchLine() string {
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

	return fit(left, m.width-ansi.StringWidth(count)) + count
}

func (m model) keyBar() string {
	if m.status != "" {
		return " " + whiteText.Render(m.status)
	}

	move := relabel(keys.Down, "↑↓", "move")

	if input := m.input(); input != nil && input.Focused() {
		return " " + m.bar.ShortHelpView([]key.Binding{move, typingKeys.Done, typingKeys.Clear})
	}

	next := tabNames[(int(m.tab)+1)%len(tabNames)]
	bindings := []key.Binding{
		move,
		keys.Search,
		relabel(keys.Open, "enter", openVerbs[m.tab]),
		keys.Copy,
		relabel(keys.NextTab, "tab", strings.ToLower(next)),
		keys.Help,
		keys.Quit,
	}

	if m.tab == joinTab {
		bindings = slices.Delete(bindings, 1, 2)
	}

	if m.showKeys {
		bindings = []key.Binding{move, relabel(keys.Help, "?", "close"), keys.Quit}
	}

	bar := m.bar
	bar.SetWidth(m.width - 1)

	return " " + bar.ShortHelpView(bindings)
}

// listRows is the current tab's visible list rows, or a notice when it has
// none to show.
func (m model) listRows(width, body int) []string {
	var rows []string

	switch m.tab {
	case eventsTab:
		if notice := feedNotice(m.eventsDone, m.eventsErr, "events"); notice != nil {
			return notice
		}

		if len(m.events) == 0 {
			return []string{" " + secondaryText.Render("Nothing scheduled right now.")}
		}

		events := m.visibleEvents()
		if len(events) == 0 {
			return emptyNotice("events")
		}

		for i := range events {
			rows = append(rows, eventRow(events, i, i == m.cursor[eventsTab], width))
		}
	case jobsTab:
		if notice := feedNotice(m.boardDone, m.boardErr, "roles"); notice != nil {
			return notice
		}

		jobs := m.visibleJobs()
		if len(jobs) == 0 {
			return emptyNotice("roles")
		}

		for i, j := range jobs {
			rows = append(rows, jobRow(j, i == m.cursor[jobsTab], width, m.now))
		}
	case chaptersTab:
		if notice := feedNotice(m.chaptersDone, m.chaptersErr, "chapters"); notice != nil {
			return notice
		}

		chapters := m.visibleChapters()
		if len(chapters) == 0 {
			return emptyNotice("chapters")
		}

		for i, c := range chapters {
			rows = append(rows, chapterRow(c, nextMeetup(m.events, c), i == m.cursor[chaptersTab], width))
		}
	default:
		for i, link := range joinLinks() {
			rows = append(rows, joinRow(link, i == m.cursor[joinTab], width))
		}
	}

	offset := m.offset[m.tab]

	return rows[min(offset, len(rows)):min(offset+body, len(rows))]
}

// detailPane is the selection's detail in three parts: top stays put (the
// map), lines scroll in the viewport under it, and actions is pinned to the
// bottom of the pane.
type detailPane struct {
	top     []string
	lines   []string
	actions string
}

// selectionDetail is the detail for the selection, `width` cells wide, with
// its text two cells narrower to leave room for the scrollbar. It is empty
// when nothing is selected.
func (m model) selectionDetail(width, body int) detailPane {
	if width <= 0 {
		return detailPane{}
	}

	text, i := width-2, m.cursor[m.tab]

	switch m.tab {
	case eventsTab:
		if events := m.visibleEvents(); m.eventsErr == nil && i < len(events) {
			lines, actions := eventDetail(events[i], text)

			return m.mapDetail(lines, actions, width, body)
		}
	case jobsTab:
		if jobs := m.visibleJobs(); m.boardErr == nil && i < len(jobs) {
			lines, actions := jobDetail(m.board, jobs[i], text)

			return detailPane{lines: lines, actions: actions}
		}
	case chaptersTab:
		if chapters := m.visibleChapters(); m.chaptersErr == nil && i < len(chapters) {
			lines, actions := chapterDetail(chapters[i], nextMeetup(m.events, chapters[i]), text)

			return m.mapDetail(lines, actions, width, body)
		}
	default:
		lines, actions := joinDetail(joinLinks()[i], text)

		return detailPane{lines: lines, actions: actions}
	}

	return detailPane{}
}

// detailHeight is the rows the viewport gets: the pane less the top and the
// actions line under a blank one.
func detailHeight(pane detailPane, body int) int {
	return max(0, body-len(pane.top)-2)
}

// syncDetail loads the selection's detail into the viewport, sized to its
// pane, and scrolls back to the top when the selection changes. Update runs
// it after every message, so a scroll key always has the detail on screen.
func (m *model) syncDetail() {
	_, width := m.paneWidths()
	pane := m.selectionDetail(width, m.bodyHeight())

	m.detail.SetWidth(max(0, width-2))
	m.detail.SetHeight(detailHeight(pane, m.bodyHeight()))
	m.detail.SetContentLines(pane.lines)

	if sel := m.selection(); sel != m.detailFor {
		m.detailFor = sel
		m.detail.GotoTop()
	}
}

// detailView is the detail pane: the key reference, or the selection's
// detail with its scrolling part from the viewport.
func (m model) detailView(width, body int) []string {
	if m.showKeys {
		return referenceLines()
	}

	pane := m.selectionDetail(width, body)
	if pane.lines == nil && pane.top == nil {
		return nil
	}

	lines := append(pane.top, m.scrolled()...)

	return append(lines, "", pane.actions)
}

// scrolled is the viewport's visible lines, with a scrollbar down the
// pane's right edge when the detail runs longer than the pane.
func (m model) scrolled() []string {
	height, total := m.detail.Height(), m.detail.TotalLineCount()
	if height <= 0 {
		return nil
	}

	lines := strings.Split(m.detail.View(), "\n")
	if total <= height {
		return lines
	}

	thumb := max(1, height*height/total)
	top := int(math.Round(float64(height-thumb) * m.detail.ScrollPercent()))

	for i := range lines {
		if i >= top && i < top+thumb {
			lines[i] += " " + linkText.Render("┃")
		} else {
			lines[i] += " " + hairlineText.Render("│")
		}
	}

	return lines
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

// action is one `key  label` of a detail pane's action line.
func action(b key.Binding, label string) string {
	return linkText.Render(b.Help().Key) + "  " + mutedText.Render(label)
}

func jobDetail(board Board, j Job, width int) (lines []string, actions string) {
	lines = wrapStyled(j.Role, width, boldText)
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

	return lines, action(keys.Open, "apply on "+hostOf(j.ApplyURL)) + "   " + action(keys.Copy, "copy link")
}

// mapDetail is a detail pane under the chapter map, with the selection's
// city lit. The map draws in during the intro and the text lands after it; a
// pane too short for both drops the map.
func (m model) mapDetail(lines []string, actions string, width, body int) detailPane {
	if m.intro < introFrames {
		lines, actions = nil, ""
	}

	rows := min(16, width*14/58, body-16)
	if rows < 6 {
		return detailPane{lines: lines, actions: actions}
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

	return detailPane{top: append(m.masks[size].render(m.chapters, frame), ""), lines: lines, actions: actions}
}

func eventDetail(e Event, width int) (lines []string, actions string) {
	lines = wrapStyled(e.Title, width, boldText)
	lines = append(lines, "", field("When", strings.TrimSpace(e.Date+"  "+deref(e.Time))), field("Chapter", cmp.Or(deref(e.City), "Online")))

	if e.Venue != nil {
		lines = append(lines, field("Venue", *e.Venue))
	}

	lines = append(lines, "")

	acts := []string{action(keys.Open, "rsvp on "+hostOf(e.URL))}

	for i, link := range e.Links {
		lines = append(lines, field(link.Label, displayURL(link.URL)))

		if i == 0 {
			acts[0] = action(keys.Open, "rsvp on "+link.Label)
		} else if b, ok := listingKey(link); ok {
			acts = append(acts, action(b, link.Label))
		}
	}

	return lines, strings.Join(append(acts, action(keys.Copy, "copy link")), "   ")
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

	return selectable(selected) + pinStyle(c).Render("●") + " " + name.Render(fit(c.Name, max(10, width-20))) + gap + when
}

func chapterDetail(c Chapter, next *Event, width int) (lines []string, actions string) {
	lines = []string{boldText.Render(c.Name), mutedText.Render(deref(c.RegionName)), ""}

	if next != nil {
		lines = append(lines, field("Next", strings.TrimSpace(next.Date+"  "+deref(next.Time))))

		// The title hangs under the date, past the label column.
		for _, line := range wrapStyled(next.Title, max(1, width-11), whiteText) {
			lines = append(lines, strings.Repeat(" ", 11)+line)
		}
	}

	acts := []string{action(keys.Open, "chapter page")}

	if c.Meetup != nil {
		lines = append(lines, field("Meetup", displayURL(*c.Meetup)))
		acts = append(acts, action(keys.Meetup, "Meetup group"))
	}

	lines = append(lines, field("Page", displayURL(c.PageURL())))

	return lines, strings.Join(append(acts, action(keys.Copy, "copy link")), "   ")
}

func joinDetail(link joinLink, width int) (lines []string, actions string) {
	lines = []string{boldText.Render(link.label), secondaryText.Render(link.about), ""}
	lines = append(lines, wrapStyled(displayURL(link.url), width, linkText)...)

	return lines, action(keys.Open, "open in your browser")
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
