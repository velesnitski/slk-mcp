package tools

import (
	"strings"

	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	slk "github.com/velesnitski/slk-mcp/internal/slack"
)

func RegisterAll(s *server.MCPServer, client *slk.Client, cfg *config.Config) {
	registerChannelTools(s, client)
	registerDigestTools(s, client)
	registerSearchTools(s, client)
	registerThreadTools(s, client, cfg.ReadOnly)
}

func resolveUsers(client *slk.Client, messages []goslack.Message) map[string]string {
	names := make(map[string]string)
	for _, msg := range messages {
		if msg.User != "" {
			if _, ok := names[msg.User]; !ok {
				names[msg.User] = client.ResolveUserName(msg.User)
			}
		}
	}
	return names
}

func detectDecisions(client *slk.Client, chName string, messages []goslack.Message, userNames map[string]string) []string {
	cfg := client.Config()
	var decisions []string

	for _, msg := range messages {
		textLower := strings.ToLower(msg.Text)
		userName := userNames[msg.User]
		if userName == "" {
			userName = msg.User
		}

		found := false
		for _, keyword := range cfg.DecisionKeywords {
			if strings.Contains(textLower, keyword) {
				decisions = append(decisions, slk.FormatDecision(msg, chName, userName, "keyword: "+keyword))
				found = true
				break
			}
		}
		if found {
			continue
		}

		reactions := make(map[string]bool)
		for _, r := range msg.Reactions {
			reactions[r.Name] = true
		}
		for _, reaction := range cfg.DecisionReactions {
			if reactions[reaction] {
				decisions = append(decisions, slk.FormatDecision(msg, chName, userName, "reaction: :"+reaction+":"))
				break
			}
		}
	}

	return decisions
}
