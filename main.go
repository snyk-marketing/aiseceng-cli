// Command aiseceng is the AI Security Engineers community in a terminal:
// upcoming meetups, open roles from the job board, and the way into Discord.
//
// Bare `aiseceng` opens the TUI (tui.go). The subcommands print and exit, for
// scripts and for anyone who would rather not drive a TUI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// version is set at release time by GoReleaser's ldflags.
var version = "dev"

const defaultJobLimit = 20

func main() {
	root := &cobra.Command{
		Use:   "aiseceng",
		Short: "The AI Security Engineers community, from your terminal",
		Long: `The AI Security Engineers community, from your terminal.

Run aiseceng on its own to browse events, jobs and chapters on a map, or
use a command below to print them and exit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
				return cmd.Help()
			}

			return runTUI(cmd.Context())
		},
	}

	root.AddCommand(eventsCmd(), jobsCmd(), chaptersCmd(), joinCmd())

	if err := fang.Execute(context.Background(), root, fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}

func eventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events",
		Short: "Show upcoming meetups across every chapter",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			events, err := fetchEvents(cmd.Context())
			if err != nil {
				return err
			}

			renderEvents(cmd.OutOrStdout(), events, termWidth())

			return nil
		},
	}
}

func jobsCmd() *cobra.Command {
	var all bool

	var limit int

	cmd := &cobra.Command{
		Use:   "jobs [search]",
		Short: "Show open roles from the job board, newest first",
		Long: `Show open roles from the job board, newest first.

Search words match the role, employer and location, and the board's tags:
region, seniority, focus area and employer type. Every word must match.`,
		Example: `  aiseceng jobs
  aiseceng jobs red team
  aiseceng jobs remote senior llm --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			board, err := fetchBoard(cmd.Context())
			if err != nil {
				return err
			}

			jobs := searchJobs(board, strings.Join(args, " "))
			total := len(jobs)

			if !all && limit < total {
				jobs = jobs[:max(limit, 0)]
			}

			renderJobs(cmd.OutOrStdout(), jobs, total, termWidth(), time.Now())

			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "show every matching role")
	cmd.Flags().IntVarP(&limit, "limit", "n", defaultJobLimit, "how many roles to show")

	return cmd
}

func chaptersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chapters [search]",
		Short: "Show every chapter and its next meetup",
		Example: `  aiseceng chapters
  aiseceng chapters emea`,
		RunE: func(cmd *cobra.Command, args []string) error {
			g, ctx := errgroup.WithContext(cmd.Context())

			var chapters []Chapter

			var events []Event

			g.Go(func() (err error) {
				chapters, err = fetchChapters(ctx)

				return err
			})
			g.Go(func() (err error) {
				events, err = fetchEvents(ctx)

				return err
			})

			if err := g.Wait(); err != nil {
				return err
			}

			chapters = searchChapters(byNextMeetup(chapters, events), strings.Join(args, " "))
			renderChapters(cmd.OutOrStdout(), chapters, events, termWidth())

			return nil
		},
	}
}

func joinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join",
		Short: "Open the community Discord in your browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			url := siteURL + "/discord"

			lipgloss.Fprintln(cmd.OutOrStdout(), linkText.Hyperlink(url).Render("→ "+url))

			return openURL(url)
		},
	}
}

// termWidth is stdout's width, or 0 when output is piped, so a pipe gets
// whole lines.
func termWidth() int {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return 0
	}

	width, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0
	}

	return width
}

func openURL(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open a browser, so open the link above yourself: %w", err)
	}

	return nil
}
