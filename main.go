package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type channelEntry struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type githubRepo struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

type brfConfig struct {
	Slack struct {
		Channels      []channelEntry `json:"channels"`
		LookbackHours int            `json:"lookback_hours"`
	} `json:"slack"`
	GitHub struct {
		Repos []githubRepo `json:"repos"`
	} `json:"github"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "brf: %v\n", err)
		os.Exit(1)
	}
}

func configPath() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "brf", "config.json"), nil
}

func loadConfig() (*brfConfig, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := &brfConfig{}
		cfg.Slack.LookbackHours = 168
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("creating config dir: %w", err)
		}
		if err := saveConfig(cfg); err != nil {
			return nil, fmt.Errorf("writing default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg brfConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Slack.LookbackHours == 0 {
		cfg.Slack.LookbackHours = 168
	}
	return &cfg, nil
}

func lookupChannelID(name string) (string, error) {
	prompt := fmt.Sprintf(
		"Search for the Slack channel named %q using slack_search_channels.\n"+
			"Return ONLY the channel ID (e.g. C01234ABCDE) and nothing else.\n"+
			"If the channel is not found, return exactly: NOT_FOUND",
		name,
	)
	result, err := runClaude(prompt, "--allowedTools", "mcp__claude_ai_Slack__slack_search_channels")
	if err != nil {
		return "", err
	}
	result = strings.TrimSpace(result)
	if result == "NOT_FOUND" {
		return "", fmt.Errorf("channel %q not found", name)
	}
	return result, nil
}

func saveConfig(cfg *brfConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func checkDeps(cfg *brfConfig) error {
	if len(cfg.GitHub.Repos) > 0 {
		if _, err := exec.LookPath("gh"); err != nil {
			return errors.New("`gh` not found on PATH — install GitHub CLI: https://cli.github.com")
		}
		if out, err := exec.Command("gh", "auth", "status").CombinedOutput(); err != nil {
			return fmt.Errorf("GitHub CLI not authenticated — run `gh auth login`\n%s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func run() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("`claude` not found on PATH — install Claude Code: https://claude.ai/code")
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := checkDeps(cfg); err != nil {
		return err
	}

	var items []item
	for _, ch := range cfg.Slack.Channels {
		items = append(items, item{
			sourceType: "slack",
			sourceID:   ch.ID,
			name:       ch.Name,
		})
	}
	for _, r := range cfg.GitHub.Repos {
		sourceID := r.Owner + "/" + r.Repo
		items = append(items, item{
			sourceType: "github",
			sourceID:   sourceID,
			name:       sourceID,
		})
	}
	header := "Project Brf — " + time.Now().Format("January 2, 2006")
	return runTUI(header, items, cfg)
}


func formatLookback(hours int) string {
	if hours > 0 && hours%168 == 0 {
		weeks := hours / 168
		if weeks == 1 {
			return "7 days"
		}
		return fmt.Sprintf("%d weeks", weeks)
	}
	if hours > 0 && hours%24 == 0 {
		days := hours / 24
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d hours", hours)
}

func buildChannelPrompt(ch channelEntry, lookbackHours int) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Today is %s.\n\n", time.Now().Format("January 2, 2006"))

	if ch.ID != "" {
		fmt.Fprintf(&sb, "Fetch recent messages from Slack channel \"%s\" (id: %s) using slack_read_channel. Cover the last %d hours.\n\n",
			ch.Name, ch.ID, lookbackHours)
	} else {
		fmt.Fprintf(&sb, "Find the Slack channel \"%s\" using slack_search_channels, then fetch its recent messages covering the last %d hours.\n\n",
			ch.Name, lookbackHours)
	}

	fmt.Fprintf(&sb, `If there is meaningful activity (decisions, progress, blockers, questions, or action items), respond with EXACTLY this format and nothing else:

### %s
**Status:** one-sentence summary of where things stand
**Recent activity:**
- bullet 1 (https://slack-permalink-url)
- bullet 2 (https://slack-permalink-url)
**Blockers / open questions:** unresolved issues with permalink, or "None"
**Action items:** things needing a decision or follow-up with permalink, or "None"

Each bullet must end with the Slack permalink for that specific message or thread in parentheses.
Use the permalink field from each message returned by slack_read_channel.

If there was no meaningful activity in the lookback window, respond with EXACTLY:

### NO_ACTIVITY

Be concise. Prefer bullets. Skip noise (emoji reactions, "+1" replies, off-topic chatter).`, ch.Name)

	return sb.String()
}

func runClaude(prompt string, extraArgs ...string) (string, error) {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = append(args, extraArgs...)
	cmd := exec.Command("claude", args...)
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var result string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var event map[string]any
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		if event["type"] == "result" {
			if s, _ := event["result"].(string); s != "" {
				result = s
			}
			if isErr, _ := event["is_error"].(bool); isErr {
				msg, _ := event["error"].(string)
				if msg == "" {
					msg = "claude returned an error"
				}
				cmd.Wait()
				return "", errors.New(msg)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	if result == "" {
		claudeStderr := strings.TrimSpace(stderrBuf.String())
		if claudeStderr != "" {
			return "", fmt.Errorf("no result from claude (stderr: %s)", claudeStderr)
		}
		return "", errors.New("no result from claude — ensure the Slack MCP is configured in Claude Code (run `claude mcp add` or check ~/.claude/settings.json)")
	}
	return result, nil
}
