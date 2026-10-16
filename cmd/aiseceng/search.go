package main

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// Search is shared by the plain commands and the TUI's `/` prompt, so
// `aiseceng jobs red team remote` and typing "red team remote" agree.
//
// Every query word must start a word somewhere in the item. Prefixes rather
// than substrings, so "us" finds the US region without matching inside
// "security", and "red team" still finds "AI Red Teaming".

// words splits text into lowercase words.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func matches(haystack []string, query []string) bool {
	return !slices.ContainsFunc(query, func(q string) bool {
		return !slices.ContainsFunc(haystack, func(word string) bool {
			return strings.HasPrefix(word, q)
		})
	})
}

// search keeps the items whose text matches query. An empty query keeps
// everything, and hands back items itself rather than a copy.
func search[T any](items []T, query string, text func(T) string) []T {
	q := words(query)
	if len(q) == 0 {
		return items
	}

	return slices.DeleteFunc(slices.Clone(items), func(item T) bool {
		return !matches(words(text(item)), q)
	})
}

// searchJobs keeps the roles matching query in their role, employer,
// location, or any tag's name: region, seniority, focus area, employer type.
func searchJobs(board Board, query string) []Job {
	return search(board.Jobs, query, func(j Job) string {
		tags := append(board.Labels(j.Regions), board.Labels(j.TechnicalDomains)...)
		tags = append(tags, board.Label(j.Seniority), board.Label(j.EmployerType))

		return j.Role + " " + j.Employer + " " + j.Location + " " + strings.Join(tags, " ")
	})
}

// searchEvents keeps the events matching query in their title, city, venue
// or date ("oct", "today").
func searchEvents(events []Event, query string) []Event {
	return search(events, query, func(e Event) string {
		return e.Title + " " + deref(e.City) + " " + deref(e.Venue) + " " + e.Date
	})
}

// searchChapters keeps the chapters matching query in their name or region
// ("lisbon", "emea", "apac").
func searchChapters(chapters []Chapter, query string) []Chapter {
	return search(chapters, query, func(c Chapter) string {
		return c.Name + " " + deref(c.Region) + " " + deref(c.RegionName)
	})
}

// nextMeetup is a chapter's soonest event, or nil when it has none
// scheduled. events are soonest first, as the feed lists them.
func nextMeetup(events []Event, c Chapter) *Event {
	for i, e := range events {
		if deref(e.Chapter) == c.Slug {
			return &events[i]
		}
	}

	return nil
}

// byNextMeetup orders chapters by their next meetup, soonest first, then the
// ones with nothing scheduled by name.
func byNextMeetup(chapters []Chapter, events []Event) []Chapter {
	next := func(c Chapter) int {
		for i, e := range events {
			if deref(e.Chapter) == c.Slug {
				return i
			}
		}

		return len(events)
	}

	return slices.SortedStableFunc(slices.Values(chapters), func(a, b Chapter) int {
		return cmp.Or(cmp.Compare(next(a), next(b)), strings.Compare(a.Name, b.Name))
	})
}

// sortNewestFirst orders roles as the site's board does: newest first, ties
// broken on role name.
func sortNewestFirst(jobs []Job) {
	slices.SortStableFunc(jobs, func(a, b Job) int {
		return cmp.Or(strings.Compare(b.PostedAt, a.PostedAt), strings.Compare(a.Role, b.Role))
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
