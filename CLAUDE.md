# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build      # compile to ./brf
make run        # build and run
make test       # go test ./...
make fmt        # go fmt ./...
make vet        # go vet ./...
make install    # install to /usr/local/bin (PREFIX overridable)
```

## Architecture

`brf` is a Go CLI that generates a Slack and GitHub activity briefing in a terminal UI.

**Execution path** (`main.go` → `tui.go` + `parse.go` + `github.go`): reads config from the XDG JSON config file, fetches each configured source in parallel by spawning `claude -p --output-format stream-json` as a subprocess (for Slack) or `gh` CLI (for GitHub), parses the stream-json to extract the final result string, then renders everything in a Bubble Tea TUI.

### Key data flow

1. `loadConfig()` in `main.go` reads channels and GitHub repos from `~/.config/brf/config.json`
2. `run()` calls `checkDeps()` to verify `gh` is on PATH and authenticated (if GitHub repos are configured), then builds a flat `[]item` list
3. The TUI triggers a `refreshStartMsg` per item; each spawns a goroutine calling either `runClaude()` (Slack) or `fetchGithubSummary()` (GitHub via `gh` CLI)
4. `runClaude()` reads `stream-json` events until a `"result"` type event arrives
5. `parseSections()` in `parse.go` splits the `### Heading` markdown Claude returns into `[]section`
6. `runTUI()` in `tui.go` displays a two-pane Bubble Tea UI: left pane = source list, right pane = scrollable content viewer; both panes show an empty-state prompt when no sources are configured

### Config format

Config lives at `$XDG_CONFIG_HOME/brf/config.json` (defaults to `~/.config/brf/config.json`). Created automatically with defaults on first run.

```json
{
  "slack": {
    "channels": [
      { "name": "channel-name", "id": "C01234ABCDE" }
    ],
    "lookback_hours": 168
  },
  "github": {
    "repos": [
      { "owner": "owner", "repo": "repo" }
    ]
  }
}
```

The in-app manage mode (`m`) adds/removes sources and writes the file back immediately. Edit the file directly for bulk changes. When adding a Slack channel by name, `lookupChannelID()` calls Claude with the `slack_search_channels` tool to resolve the ID automatically.

### Runtime requirements

- `claude` CLI must be on PATH with the Slack MCP (`mcp__claude_ai_Slack__*`) configured — connect Slack at claude.ai Settings → Integrations
- `gh` CLI must be on PATH and authenticated (`gh auth login`) for GitHub sources — checked by `checkDeps()` before the TUI launches

### TUI keybindings

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Switch between list and content panes |
| `↑`/`↓` or `j`/`k` | Navigate list |
| `enter`/`l`/`→` | Focus content pane |
| `o` | Open active source link in browser |
| `r` | Refresh all sources |
| `m` | Open manage mode (add/remove sources) |
| `q` / `ctrl+c` | Quit |

### TUI styling

All colors are defined at the top of `tui.go` using the Tokyo Night palette. `renderContent()` does lightweight markdown colorization (bold labels, bullet points, active-link highlighting) without a full markdown renderer.
