package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type channelEntry struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type githubRepo struct {
	Owner string
	Repo  string
}

type briefConfig struct {
	Slack struct {
		Channels      []channelEntry `json:"channels"`
		LookbackHours int            `json:"lookback_hours"`
	} `json:"slack"`
	GitHub struct {
		Repos []githubRepo
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "brief: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("`claude` not found on PATH — install Claude Code")
	}

	path, err := dbPath()
	if err != nil {
		return fmt.Errorf("finding db path: %w", err)
	}

	store, err := openStore(path)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}

	cfg, err := store.loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var items []item
	for _, ch := range cfg.Slack.Channels {
		it := item{
			sourceType: "slack",
			sourceID:   ch.ID,
			name:       ch.Name,
		}
		if summary, fetchedAt, ok := store.getCachedSummary("slack", ch.ID); ok {
			it.summary = summary
			it.fetchedAt = fetchedAt
		}
		items = append(items, it)
	}
	for _, r := range cfg.GitHub.Repos {
		sourceID := r.Owner + "/" + r.Repo
		it := item{
			sourceType: "github",
			sourceID:   sourceID,
			name:       sourceID,
		}
		if summary, fetchedAt, ok := store.getCachedSummary("github", sourceID); ok {
			it.summary = summary
			it.fetchedAt = fetchedAt
		}
		items = append(items, it)
	}

	header := "Project Brief — " + time.Now().Format("January 2, 2006")
	return runTUI(header, items, cfg, store)
}

func logRaw(name, text string, err error) {
	path := fmt.Sprintf("/tmp/brief-%s.log", strings.NewReplacer("/", "-", " ", "-").Replace(name))
	var content string
	if err != nil {
		content = fmt.Sprintf("ERROR: %v\n", err)
	} else {
		content = text
	}
	os.WriteFile(path, []byte(content), 0644) //nolint
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
- bullet 1
- bullet 2
**Blockers / open questions:** unresolved issues, or "None"
**Action items:** things needing a decision or follow-up, or "None"

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
		return "", errors.New("no result from claude — check if Slack MCP is configured")
	}
	return result, nil
}
