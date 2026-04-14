package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Token             string
	Channels          []string
	ReadOnly          bool
	DisabledTools     map[string]bool
	DigestHours       int
	DecisionKeywords  []string
	DecisionReactions []string
}

func Load() *Config {
	channels := parseCSV(os.Getenv("SLACK_CHANNELS"))
	readOnly := parseBool(os.Getenv("SLACK_READ_ONLY"))
	disabled := parseCSV(os.Getenv("DISABLED_TOOLS"))
	disabledMap := make(map[string]bool, len(disabled))
	for _, t := range disabled {
		disabledMap[strings.ToLower(strings.ReplaceAll(t, "-", "_"))] = true
	}

	digestHours, _ := strconv.Atoi(os.Getenv("SLACK_DIGEST_HOURS"))
	if digestHours <= 0 {
		digestHours = 24
	}

	return &Config{
		Token:         os.Getenv("SLACK_TOKEN"),
		Channels:      channels,
		ReadOnly:      readOnly,
		DisabledTools: disabledMap,
		DigestHours:   digestHours,
		DecisionKeywords: []string{
			"decided", "approved", "let's go with", "agreed",
			"confirmed", "moving forward", "final answer",
		},
		DecisionReactions: []string{
			"white_check_mark", "heavy_check_mark", "eyes", "thumbsup",
		},
	}
}

func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "1" || s == "true" || s == "yes"
}
