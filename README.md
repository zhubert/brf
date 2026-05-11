# brf

A terminal briefing tool that summarizes recent Slack and GitHub activity using Claude AI. Run it each morning to get a structured digest of what happened across your channels and repos.

```
╭──────────────────────────────╮╭────────────────────────────────────────────────╮
│ ── Slack ──                  ││ **Status:** Active discussion on the new auth   │
│ ▸   #engineering             ││ rollout, migration plan finalized.              │
│     #product                 ││                                                 │
│     #incidents               ││ **Recent activity:**                            │
│ ── GitHub Repos ──           ││   • @alice merged the session token PR          │
│     myorg/api                ││   • Deployment scheduled for Thursday           │
│     myorg/frontend           ││   • @bob raised a question about rate limits    │
│                              ││                                                 │
│                              ││ **Blockers / open questions:**                  │
│                              ││ Rate limit behavior needs clarification before  │
│                              ││ Thursday deploy.                                │
╰──────────────────────────────╯╰────────────────────────────────────────────────╯
  brf · May 11, 2026  tab switch pane  ↑↓/jk navigate  r refresh  m manage  q quit
```

## Quick start

### 1. Install brf

```bash
go install github.com/zhubert/brf@latest
```

Or build from source:

```bash
git clone https://github.com/zhubert/brf
cd brf
make install   # installs to /usr/local/bin; override with PREFIX=/your/path make install
```

### 2. Install Claude Code

`brf` uses the `claude` CLI to read Slack and summarize content. Install it from [claude.ai/code](https://claude.ai/code) and make sure it's on your PATH.

### 3. Connect Slack to Claude Code

`brf` reads Slack via Claude Code's built-in Slack integration. To enable it:

1. Open [claude.ai](https://claude.ai) and go to **Settings → Integrations**
2. Connect your Slack workspace
3. Verify it works: `claude -p "list my slack channels" --allowedTools mcp__claude_ai_Slack__slack_search_channels`

### 4. Install the GitHub CLI (for GitHub sources)

```bash
brew install gh   # or see https://cli.github.com
gh auth login
```

### 5. Run brf

```bash
brf
```

On first run, `brf` creates an empty config at `~/.config/brf/config.json`. Press `m` to open manage mode and add your first Slack channel or GitHub repo.

---

## Configuration

Config is stored at `~/.config/brf/config.json` (respects `$XDG_CONFIG_HOME`).

```json
{
  "slack": {
    "channels": [
      { "name": "engineering", "id": "C01234ABCDE" }
    ],
    "lookback_hours": 168
  },
  "github": {
    "repos": [
      { "owner": "myorg", "repo": "api" }
    ]
  }
}
```

Edit the file directly for bulk changes, or use the in-app manage mode (`m`) to add and remove sources interactively. When adding a Slack channel by name, `brf` automatically looks up the channel ID via Claude — you don't need to find the ID yourself.

## Keybindings

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Switch between list and content panes |
| `↑`/`↓` or `j`/`k` | Navigate source list |
| `enter` / `l` / `→` | Focus content pane |
| `o` | Open source link in browser |
| `r` | Refresh all sources |
| `m` | Manage sources (add/remove) |
| `q` / `ctrl+c` | Quit |

## How it works

- **Slack channels** — Claude reads the channel via the Slack MCP and returns a structured summary: status, recent activity, blockers, and action items.
- **GitHub repos** — `gh pr list` fetches recent pull requests; Claude summarizes what's being worked on and what has shipped.

All sources load in parallel on startup and can be refreshed with `r`.

## License

MIT
