package main

import "strings"

type section struct {
	title      string
	content    string
	noActivity bool
}

func parseSections(text string) (string, []section) {
	var header string
	var sections []section

	lines := strings.Split(text, "\n")
	var currentTitle string
	var currentLines []string
	inSection := false

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			header = strings.TrimPrefix(line, "## ")
			continue
		}
		if strings.HasPrefix(line, "### ") {
			if inSection {
				sections = append(sections, makeSection(currentTitle, currentLines))
			}
			currentTitle = strings.TrimPrefix(line, "### ")
			currentLines = nil
			inSection = true
			continue
		}
		if inSection {
			currentLines = append(currentLines, line)
		}
	}
	if inSection && currentTitle != "" {
		sections = append(sections, makeSection(currentTitle, currentLines))
	}

	return header, sections
}

func makeSection(title string, lines []string) section {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// trim leading blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	content := strings.Join(lines, "\n")
	noActivity := strings.EqualFold(strings.TrimSpace(title), "no recent activity")
	return section{
		title:      strings.TrimSpace(title),
		content:    content,
		noActivity: noActivity,
	}
}
