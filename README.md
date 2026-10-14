# aiseceng

The [AI Security Engineers](https://www.aiseceng.io) community, from your terminal: upcoming meetups across every chapter, open roles from the job board, and every chapter on a map.

Built with Charm's [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [Fang](https://github.com/charmbracelet/fang).

## Install

```sh
brew install snyk-marketing/tap/aiseceng
```

Or with Go:

```sh
go install github.com/snyk-marketing/aiseceng-cli/cmd/aiseceng@latest
```

Binaries for macOS, Linux and Windows are on the [releases page](https://github.com/snyk-marketing/aiseceng-cli/releases).

## Use

Run `aiseceng` on its own for the TUI: Events, Jobs, Chapters and Join tabs, each a list with the selected item's detail beside it. On Events and Chapters the detail sits under a map of every chapter, with the selected city lit.

| Key | Does |
| :-- | :-- |
| `↑` `↓` or `j` `k` | Move |
| `tab`, `←` `→`, `1`-`4` | Switch tabs |
| `/` | Search, as you type |
| `enter` | Open the RSVP, the apply link or the chapter page |
| `l` / `m` | Open an event on Luma or Meetup, or a chapter's Meetup group |
| `c` | Copy the link |
| `esc` | Clear the search |
| `q` | Quit |

Or print and exit, for scripts and for anyone who would rather not use a TUI:

```sh
aiseceng events                   # upcoming meetups across every chapter
aiseceng jobs remote senior llm   # open roles, filtered by search words
aiseceng chapters emea            # chapters and their next meetup
aiseceng join                     # open the Discord
```

Search words match the start of any word in a role, employer or location, or in a tag's name: region, seniority, focus area, employer type. Every word must match.

## Where the data comes from

| | Source |
| :-- | :-- |
| Events | `www.aiseceng.io/api/events.json`: the Luma calendar and every chapter's Meetup group, merged as the site's events page lists them |
| Chapters | `www.aiseceng.io/api/chapters.json` |
| Jobs | The job board's public API, the one the site's job board reads |

Two environment variables point it elsewhere, for development: `AISECENG_URL` (the site, such as `http://localhost:4321`) and `AISECENG_JOBS_API_URL`.

## Develop

```sh
go run ./cmd/aiseceng          # the TUI
go run ./cmd/aiseceng events   # a command
go test ./...
```

## Release

Push a tag such as `v0.1.0`. The release workflow runs [GoReleaser](https://goreleaser.com), which publishes the binaries here and updates the cask in [snyk-marketing/homebrew-tap](https://github.com/snyk-marketing/homebrew-tap). It pushes the cask with the `HOMEBREW_TAP_DEPLOY_KEY` secret, the private half of a read-write deploy key on that repo.

## License

[MIT](LICENSE)
