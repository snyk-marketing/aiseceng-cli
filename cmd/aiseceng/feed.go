package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// siteURL serves the events feed and every link the CLI opens, so a calendar
// or Discord invite can move without a new binary. AISECENG_URL points it
// elsewhere, such as a local `pnpm dev`.
var siteURL = strings.TrimRight(cmp.Or(os.Getenv("AISECENG_URL"), "https://www.aiseceng.io"), "/")

// jobsAPIURL is aiseceng-job-board-worker's public API, the one the site's job
// board reads. AISECENG_JOBS_API_URL overrides it.
var jobsAPIURL = strings.TrimRight(cmp.Or(os.Getenv("AISECENG_JOBS_API_URL"), "https://aiseceng-job-board-api.onrender.com"), "/")

// Event is one entry of the site's /api/events.json.
type Event struct {
	Title string `json:"title"`
	// URL is the RSVP: Luma's page when Luma lists the event.
	URL string `json:"url"`
	// Links is every listing of the event, the RSVP first: one event Meetup and
	// Luma both list has a link for each.
	Links []EventLink `json:"links"`
	City  *string     `json:"city"`
	// Chapter is the chapter's slug, as /api/chapters.json lists it.
	Chapter  *string   `json:"chapter"`
	Venue    *string   `json:"venue"`
	StartsAt time.Time `json:"startsAt"`
	Date     string    `json:"date"`
	Time     *string   `json:"time"`
}

// Chapter is one entry of the site's /api/chapters.json.
type Chapter struct {
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	Region     *string `json:"region"`
	RegionName *string `json:"regionName"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	// Meetup is the chapter's Meetup group page.
	Meetup *string `json:"meetup"`
}

// City is the chapter's name without its country: "Boston, MA" is Boston.
func (c Chapter) City() string {
	city, _, _ := strings.Cut(c.Name, ",")

	return city
}

// PageURL is the chapter's page on the site.
func (c Chapter) PageURL() string {
	return siteURL + "/chapters/" + c.Slug + "/"
}

// EventLink is one listing of an event, labelled "Luma", "Meetup", or with
// the host of anywhere else.
type EventLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Job is one role from the job board API's /v1/jobs. The facet fields hold
// slugs (`remote`, `llm-security`); Board.Label names them.
type Job struct {
	ID               string   `json:"id"`
	Role             string   `json:"role"`
	Employer         string   `json:"employer"`
	Location         string   `json:"location"`
	Description      string   `json:"description"`
	Regions          []string `json:"regions"`
	Seniority        string   `json:"seniority"`
	EmployerType     string   `json:"employerType"`
	TechnicalDomains []string `json:"technicalDomains"`
	ApplyURL         string   `json:"applyUrl"`
	PostedAt         string   `json:"postedAt"`
}

// Board is every published role plus the names of the tags they carry.
type Board struct {
	Jobs   []Job
	labels map[string]string
}

// Label names a facet slug ("llm-security" is "LLM Security"), falling back
// to the slug for a tag the vocabulary does not list.
func (b Board) Label(slug string) string {
	return cmp.Or(b.labels[slug], slug)
}

func (b Board) Labels(slugs []string) []string {
	names := make([]string, len(slugs))
	for i, slug := range slugs {
		names[i] = b.Label(slug)
	}

	return names
}

var client = &http.Client{Timeout: 15 * time.Second}

// getJSON GETs url and decodes it into T. A failed attempt is retried once:
// the job API runs on a Render instance that restarts on deploy, so one
// refused connection is expected rather than an outage.
func getJSON[T any](ctx context.Context, url string) (T, error) {
	var body T

	var err error

	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return body, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}

		if body, err = getOnce[T](ctx, url); err == nil {
			return body, nil
		}
	}

	return body, err
}

func getOnce[T any](ctx context.Context, url string) (T, error) {
	var body T

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return body, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aiseceng-cli/"+cmp.Or(version, "dev"))

	res, err := client.Do(req)
	if err != nil {
		return body, fmt.Errorf("could not reach %s: %w", req.URL.Host, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return body, fmt.Errorf("%s answered %s", url, res.Status)
	}

	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return body, fmt.Errorf("%s sent something unreadable: %w", url, err)
	}

	return body, nil
}

func fetchEvents(ctx context.Context) ([]Event, error) {
	feed, err := getJSON[struct{ Events []Event }](ctx, siteURL+"/api/events.json")

	return feed.Events, err
}

func fetchChapters(ctx context.Context) ([]Chapter, error) {
	feed, err := getJSON[struct{ Chapters []Chapter }](ctx, siteURL+"/api/chapters.json")

	return feed.Chapters, err
}

// jobPageSize is the API's largest page.
const jobPageSize = 100

// jobPagesAtOnce is how many pages of the board are read side by side.
const jobPagesAtOnce = 4

// fetchBoard reads every role alongside the tag vocabulary.
func fetchBoard(ctx context.Context) (Board, error) {
	board := Board{labels: map[string]string{}}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		labels, err := getJSON[struct {
			Facets []struct {
				Values []struct{ Name, Slug string }
			}
		}](ctx, jobsAPIURL+"/v1/labels")

		for _, facet := range labels.Facets {
			for _, value := range facet.Values {
				board.labels[value.Slug] = value.Name
			}
		}

		return err
	})

	g.Go(func() (err error) {
		board.Jobs, err = fetchJobs(ctx)

		return err
	})

	if err := g.Wait(); err != nil {
		return Board{}, err
	}

	sortNewestFirst(board.Jobs)

	return board, nil
}

type jobPage struct {
	Items      []Job
	Pagination *struct{ Total int }
}

func fetchJobPage(ctx context.Context, offset int) (jobPage, error) {
	return getJSON[jobPage](ctx, fmt.Sprintf("%s/v1/jobs?limit=%d&offset=%d", jobsAPIURL, jobPageSize, offset))
}

// fetchJobs reads every role: the first page for the count, then the rest
// side by side, jobPagesAtOnce at a time.
func fetchJobs(ctx context.Context) ([]Job, error) {
	first, err := fetchJobPage(ctx, 0)
	if err != nil || len(first.Items) == 0 {
		return nil, err
	}

	// Without a count, a full page could be the whole board or its first
	// slice, and showing a slice as the board would be a quiet lie.
	if first.Pagination == nil {
		return nil, fmt.Errorf("%s/v1/jobs left out pagination.total, so the board may be incomplete", jobsAPIURL)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(jobPagesAtOnce)

	// An empty page ends the board whatever `total` claimed, so a count the
	// pages cannot satisfy stops the reads rather than running on.
	var ended atomic.Bool

	// Pages step by the first page's size, in case the API serves fewer than
	// asked for. Each read fills its own slot, kept in board order.
	step := len(first.Items)
	pages := []*[]Job{&first.Items}

	for offset := step; offset < first.Pagination.Total && !ended.Load(); offset += step {
		page := new([]Job)
		pages = append(pages, page)

		g.Go(func() error {
			next, err := fetchJobPage(ctx, offset)
			if err == nil && len(next.Items) == 0 {
				ended.Store(true)
			}

			*page = next.Items

			return err
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	var jobs []Job
	for _, page := range pages {
		jobs = append(jobs, *page...)
	}

	return jobs, nil
}
