# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build      # compile to ./brief
make run        # build and run
make test       # go test ./...
make fmt        # go fmt ./...
make vet        # go vet ./...
make install    # install to /usr/local/bin (PREFIX overridable)
```

## Architecture

`brief` is a Go CLI that generates a Slack activity briefing in a terminal UI. It has two execution paths:

**Compiled binary** (`main.go` → `tui.go` + `parse.go`): reads `brief-config.json`, fetches each configured Slack channel in parallel by spawning `claude -p --output-format stream-json --allowedTools slack_read_channel,slack_search_channels` as a subprocess, parses the stream-json to extract the final result string, then renders everything in a Bubble Tea TUI.

**Slash command** (`.claude/commands/brief.md`): an alternative that runs the same briefing logic directly inside Claude Code using the Slack MCP — no compilation needed. The `/brief` command reads `brief-config.json` itself and calls `slack_read_channel` directly.

### Key data flow

1. `loadConfig()` reads `./brief-config.json` or `~/.claude/brief-config.json`
2. `fetchAllChannels()` fans out goroutines — one per channel — each calling `runClaude()` which spawns the `claude` subprocess and reads `stream-json` events until a `"result"` type event arrives
3. `parseSections()` in `parse.go` splits the `### Heading` markdown Claude returns into `[]section`
4. `runTUI()` in `tui.go` displays a two-pane Bubble Tea UI: left pane = channel list, right pane = scrollable content viewer

### Config format

```json
{
  "slack": {
    "channels": [
      { "name": "channel-name", "id": "SLACK_CHANNEL_ID" }
    ],
    "lookback_hours": 168
  }
}
```

Channel `id` is preferred (skips a search round-trip). If omitted, `brief` falls back to `slack_search_channels`.

### Runtime requirement

The `claude` CLI must be on PATH with the Slack MCP (`mcp__claude_ai_Slack__*`) configured. Raw Claude output per channel is logged to `/tmp/brief-<channel-name>.log` for debugging.

### TUI styling

All colors are defined at the top of `tui.go` using the Tokyo Night palette. `renderContent()` does lightweight markdown colorization (bold labels, bullet points, blocker highlighting) without a full markdown renderer.
