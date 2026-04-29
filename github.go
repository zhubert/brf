package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type ghPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	MergedAt  *time.Time `json:"mergedAt"`
	State     string    `json:"state"`
}

func fetchGithubSummary(owner, repo string, lookbackHours int) (string, error) {
	out, err := exec.Command("gh", "pr", "list",
		"--repo", owner+"/"+repo,
		"--state", "all",
		"--json", "number,title,body,author,createdAt,mergedAt,state",
		"--limit", "200",
	).Output()
	if err != nil {
		return "", fmt.Errorf("gh pr list: %w", err)
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", fmt.Errorf("parsing gh output: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(lookbackHours) * time.Hour)
	var relevant []ghPR
	for _, pr := range prs {
		inWindow := pr.CreatedAt.After(cutoff) ||
			(pr.MergedAt != nil && pr.MergedAt.After(cutoff))
		if inWindow {
			relevant = append(relevant, pr)
		}
	}

	if len(relevant) == 0 {
		return fmt.Sprintf("No pull request activity in the last %s.", formatLookback(lookbackHours)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Today is %s.\n\n", time.Now().Format("January 2, 2006"))
	fmt.Fprintf(&sb, "Here are pull requests for %s/%s from the last %d hours:\n\n", owner, repo, lookbackHours)

	for _, pr := range relevant {
		status := pr.State
		if pr.MergedAt != nil {
			status = "merged"
		}
		fmt.Fprintf(&sb, "PR #%d [%s]: %s (by %s)\n", pr.Number, status, pr.Title, pr.Author.Login)
		if pr.Body != "" {
			body := pr.Body
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			fmt.Fprintf(&sb, "Description: %s\n", body)
		}
		fmt.Fprintln(&sb)
	}

	sb.WriteString(`Summarize these pull requests at a high level. What's being worked on? What has shipped? Any notable patterns or themes across PRs?

Respond with EXACTLY this format:

### ` + owner + `/` + repo + `
**Status:** one-sentence summary of overall state
**What's being worked on:**
- bullet 1
- bullet 2
**What shipped:**
- bullet 1 (or "Nothing merged in this window")
**Notable patterns:** observations about the work, or "None"`)

	return runClaude(sb.String())
}
