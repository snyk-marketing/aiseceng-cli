package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var testBoard = Board{
	Jobs: []Job{
		{Role: "AI Red Team Engineer", Employer: "Mercor", Location: "Anywhere", Regions: []string{"remote"}},
		{Role: "Security Engineer", Employer: "Sezzle", Location: "Austin", Regions: []string{"us"}, TechnicalDomains: []string{"llm-security"}},
		{Role: "Security Engineer", Employer: "Monzo", Location: "London", Regions: []string{"uk"}},
	},
	labels: map[string]string{"remote": "Remote", "us": "US", "uk": "UK", "llm-security": "LLM Security"},
}

func employers(jobs []Job) string {
	names := make([]string, len(jobs))
	for i, j := range jobs {
		names[i] = j.Employer
	}

	return strings.Join(names, ",")
}

func TestSearchJobsMatchesWordStartsAcrossTextAndTags(t *testing.T) {
	cases := map[string]string{
		"":                "Mercor,Sezzle,Monzo",
		"red team":        "Mercor",
		"RED TEAM remote": "Mercor",
		"us":              "Sezzle",
		"llm":             "Sezzle",
		"security uk":     "Monzo",
	}

	for query, want := range cases {
		if got := employers(searchJobs(testBoard, query)); got != want {
			t.Errorf("searchJobs(%q) = %s, want %s", query, got, want)
		}
	}
}

func TestRenderTableFitsTheTerminal(t *testing.T) {
	var out bytes.Buffer

	renderTable(&out, [][]cell{
		{{text: "14 Oct"}, {text: strings.Repeat("Engineer ", 20)}, {text: "Sezzle"}},
	}, 1, 60)

	line := strings.TrimRight(out.String(), "\n")

	if width := ansi.StringWidth(line); width != 60 {
		t.Fatalf("want a 60-column line, got %d: %q", width, line)
	}

	if !strings.Contains(line, "…  Sezzle") {
		t.Fatalf("want the flex column truncated before the next one, got %q", line)
	}
}

func TestRenderJobsPrintsEachApplyLinkUnderItsRole(t *testing.T) {
	var out bytes.Buffer

	jobs := []Job{
		{Role: "AI Red Team Engineer", Employer: "Mercor", PostedAt: "2026-10-13", ApplyURL: "https://jobs.example.com/red-team"},
		{Role: "Security Engineer", Employer: "Monzo", PostedAt: "2026-10-12", ApplyURL: "https://jobs.example.com/security"},
	}

	renderJobs(&out, jobs, 2, 40, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))

	lines := strings.Split(ansi.Strip(out.String()), "\n")

	for i, line := range lines {
		if strings.Contains(line, "AI Red Team") {
			if next := strings.TrimSpace(lines[i+1]); next != "https://jobs.example.com/red-team" {
				t.Fatalf("want the whole link under the role even at 40 columns, got %q", next)
			}

			return
		}
	}

	t.Fatalf("no role row in:\n%s", out.String())
}

func TestRenderEventsPrintsEveryListingUnderItsEvent(t *testing.T) {
	var out bytes.Buffer

	renderEvents(&out, []Event{{
		Title: "AI Universal Celebration Hackathon",
		Date:  "Mon 19 Oct",
		Links: []EventLink{
			{Label: "Luma", URL: "https://luma.com/lfjtdq6b"},
			{Label: "Meetup", URL: "https://www.meetup.com/aiseceng-sf/events/1/"},
		},
	}}, 40)

	view := ansi.Strip(out.String())

	if !strings.Contains(view, "\n            https://luma.com/lfjtdq6b\n            https://www.meetup.com/aiseceng-sf/events/1/\n") {
		t.Fatalf("want both listings whole under the event, past the date column, got:\n%s", view)
	}
}

func TestRenderChaptersPrintsEachPageUnderItsChapter(t *testing.T) {
	var out bytes.Buffer

	renderChapters(&out, []Chapter{{Slug: "aiseceng-lisbon", Name: "Lisbon, PT"}}, nil, 40)

	if view := ansi.Strip(out.String()); !strings.Contains(view, "Lisbon, PT") || !strings.Contains(view, "\n            "+siteURL+"/chapters/aiseceng-lisbon/\n") {
		t.Fatalf("want the chapter page whole under the chapter, past the name column, got:\n%s", view)
	}
}

// Every animation must end: the launch (banner, map, cascade) and the ping
// when the cursor lands on another city. Once each has played the TUI stops
// asking for frames, so an idle TUI redraws nothing.
func TestTUIAnimationsSettle(t *testing.T) {
	lisbon, sf := "aiseceng-lisbon", "aiseceng-san-francisco"

	var m tea.Model = newModel(context.Background())

	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(eventsMsg{events: []Event{
		{Title: "Hack night", Date: "Thu 22 Oct", Chapter: &lisbon},
		{Title: "Hackathon", Date: "Fri 30 Oct", Chapter: &sf},
	}})
	m, _ = m.Update(boardMsg{board: testBoard})
	m, _ = m.Update(chaptersMsg{chapters: []Chapter{
		{Slug: lisbon, Name: "Lisbon, PT", Lat: 38.7, Lon: -9.1},
		{Slug: sf, Name: "San Francisco, CA", Lat: 37.8, Lon: -122.4},
	}})

	settle := func(cmd tea.Cmd, budget int) {
		t.Helper()

		for frames := 0; cmd != nil; frames++ {
			if frames > budget {
				t.Fatalf("still animating after %d frames", frames)
			}

			m, cmd = m.Update(frameMsg{})
		}

		if got := m.(model); got.inSplash || got.ticking {
			t.Fatalf("want the main view, idle; got inSplash=%v ticking=%v", got.inSplash, got.ticking)
		}
	}

	settle(nextFrame(), splashFrames+introFrames+pingFrames+1)

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Jobs 3") || !strings.Contains(view, "Lisbon") {
		t.Fatalf("want the tab bar to count the board's 3 roles and the map to name Lisbon, got:\n%s", view)
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.(model).ping != pingFrames {
		t.Fatal("want moving onto San Francisco to ping it")
	}

	settle(cmd, pingFrames+1)
}

// ? swaps the detail for the key reference, and esc puts the detail back.
func TestTUIKeyReference(t *testing.T) {
	var m tea.Model = newModel(context.Background())

	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(eventsMsg{})
	m, _ = m.Update(boardMsg{board: testBoard})
	m, _ = m.Update(chaptersMsg{})

	for range splashFrames + introFrames {
		m, _ = m.Update(frameMsg{})
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	m, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "search as you type") || strings.Contains(view, "apply on") {
		t.Fatalf("want ? to swap the role's detail for the key reference, got:\n%s", view)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "search as you type") || !strings.Contains(view, "apply on") {
		t.Fatalf("want esc to bring the role's detail back, got:\n%s", view)
	}
}
