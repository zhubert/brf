package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	seeded, err := s.isSeeded()
	if err != nil {
		db.Close()
		return nil, err
	}
	if !seeded {
		if err := s.seedFromConfig(); err != nil {
			db.Close()
			return nil, err
		}
		if err := s.markSeeded(); err != nil {
			db.Close()
			return nil, err
		}
	}

	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS slack_channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			enabled INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS github_repos (
			owner TEXT NOT NULL,
			repo TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			PRIMARY KEY (owner, repo)
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS summaries (
			source_type TEXT NOT NULL,
			source_id TEXT NOT NULL,
			summary TEXT,
			fetched_at TEXT NOT NULL,
			PRIMARY KEY (source_type, source_id)
		);
	`)
	return err
}

func (s *Store) isSeeded() (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'seeded'`).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return v == "1", err
}

func (s *Store) markSeeded() error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('seeded', '1')`)
	return err
}

func (s *Store) seedFromConfig() error {
	data, err := os.ReadFile("brief-config.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var raw struct {
		Slack struct {
			Channels      []channelEntry `json:"channels"`
			LookbackHours int            `json:"lookback_hours"`
		} `json:"slack"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing brief-config.json: %w", err)
	}

	for _, ch := range raw.Slack.Channels {
		if err := s.addSlackChannel(ch.ID, ch.Name); err != nil {
			return err
		}
	}

	if raw.Slack.LookbackHours > 0 {
		_, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('lookback_hours', ?)`,
			strconv.Itoa(raw.Slack.LookbackHours))
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) loadConfig() (*briefConfig, error) {
	cfg := &briefConfig{}

	rows, err := s.db.Query(`SELECT id, name FROM slack_channels WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch channelEntry
		if err := rows.Scan(&ch.ID, &ch.Name); err != nil {
			return nil, err
		}
		cfg.Slack.Channels = append(cfg.Slack.Channels, ch)
	}

	repoRows, err := s.db.Query(`SELECT owner, repo FROM github_repos WHERE enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer repoRows.Close()
	for repoRows.Next() {
		var r githubRepo
		if err := repoRows.Scan(&r.Owner, &r.Repo); err != nil {
			return nil, err
		}
		cfg.GitHub.Repos = append(cfg.GitHub.Repos, r)
	}

	var lookbackStr string
	err = s.db.QueryRow(`SELECT value FROM settings WHERE key = 'lookback_hours'`).Scan(&lookbackStr)
	if err == nil {
		cfg.Slack.LookbackHours, _ = strconv.Atoi(lookbackStr)
	}
	if cfg.Slack.LookbackHours == 0 {
		cfg.Slack.LookbackHours = 168
	}

	return cfg, nil
}

func (s *Store) getCachedSummary(sourceType, sourceID string) (summary string, fetchedAt time.Time, ok bool) {
	var fetchedStr string
	err := s.db.QueryRow(
		`SELECT summary, fetched_at FROM summaries WHERE source_type = ? AND source_id = ?`,
		sourceType, sourceID,
	).Scan(&summary, &fetchedStr)
	if err != nil {
		return "", time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, fetchedStr)
	if err != nil {
		t = time.Time{}
	}
	return summary, t, true
}

func (s *Store) saveSummary(sourceType, sourceID, summary string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO summaries (source_type, source_id, summary, fetched_at) VALUES (?, ?, ?, ?)`,
		sourceType, sourceID, summary, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) addSlackChannel(id, name string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO slack_channels (id, name, enabled) VALUES (?, ?, 1)`,
		id, name,
	)
	return err
}

func (s *Store) removeSlackChannel(id string) error {
	_, err := s.db.Exec(`DELETE FROM slack_channels WHERE id = ?`, id)
	return err
}

func (s *Store) addGithubRepo(owner, repo string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO github_repos (owner, repo, enabled) VALUES (?, ?, 1)`,
		owner, repo,
	)
	return err
}

func (s *Store) removeGithubRepo(owner, repo string) error {
	_, err := s.db.Exec(`DELETE FROM github_repos WHERE owner = ? AND repo = ?`, owner, repo)
	return err
}

func dbPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "personal", "briefing", "brief.db"), nil
}
