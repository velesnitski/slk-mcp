package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
)

func registerUserTools(s *server.MCPServer, d Deps) {
	if d.Cfg.IsDisabled("list_users") {
		return
	}
	s.AddTool(
		mcp.NewTool("list_users",
			mcp.WithDescription("List active workspace users with handle, real name, job title, role flags, and profile-update date. Optionally include last-message date."),
			mcp.WithBoolean("include_bots", mcp.Description("Include bot/integration accounts (default: false)")),
			mcp.WithBoolean("with_activity", mcp.Description("Fetch each user's last-message date via search (slower; one search.messages call per user, run in parallel) (default: false)")),
			mcp.WithString("filter", mcp.Description("Case-insensitive substring filter — matches against handle, real name, and job title. Useful for 'marketing', 'qa', 'devops' etc.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			includeBots := req.GetBool("include_bots", false)
			withActivity := req.GetBool("with_activity", false)
			filter := strings.ToLower(strings.TrimSpace(req.GetString("filter", "")))

			users, err := d.Client.Users.List(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

			filtered := make([]goslack.User, 0, len(users))
			for _, u := range users {
				if (u.IsBot || u.Name == "slackbot") && !includeBots {
					continue
				}
				if filter != "" && !userMatchesFilter(u, filter) {
					continue
				}
				filtered = append(filtered, u)
			}

			lastPost := map[string]string{}
			if withActivity {
				lastPost = fetchLastPostDates(ctx, d, filtered)
			}

			var b strings.Builder
			for _, u := range filtered {
				flags := ""
				switch {
				case u.IsAdmin:
					flags = " admin"
				case u.IsOwner:
					flags = " owner"
				case u.IsRestricted:
					flags = " guest"
				}
				if u.IsBot {
					flags += " bot"
				}
				real := u.RealName
				if real == "" {
					real = u.Profile.RealName
				}
				if real == "" {
					real = u.Profile.DisplayName
				}
				if real == "" {
					real = "(no name)"
				}
				updated := u.Updated.Time().UTC().Format("2006-01-02")
				title := strings.TrimSpace(u.Profile.Title)
				if withActivity {
					last := lastPost[u.ID]
					if last == "" {
						last = "(none found)"
					}
					fmt.Fprintf(&b, "%s | %s | %s |%s | profile_updated=%s | last_post=%s\n",
						u.Name, real, title, flags, updated, last)
				} else {
					fmt.Fprintf(&b, "%s | %s | %s |%s | profile_updated=%s\n",
						u.Name, real, title, flags, updated)
				}
			}
			header := fmt.Sprintf("%d users\n", len(filtered))
			return mcp.NewToolResultText(header + strings.TrimRight(b.String(), "\n")), nil
		},
	)
}

// userMatchesFilter returns true if needle (already lowercased) is a
// substring of the user's handle, real name, display name, or job title.
func userMatchesFilter(u goslack.User, needle string) bool {
	hay := strings.ToLower(u.Name + " " + u.RealName + " " + u.Profile.RealName + " " + u.Profile.DisplayName + " " + u.Profile.Title)
	return strings.Contains(hay, needle)
}

// fetchLastPostDates queries search.messages with from:@handle for
// each user and returns a map of user ID → "YYYY-MM-DD" of the most
// recent hit. Empty string when there's no match.
func fetchLastPostDates(ctx context.Context, d Deps, users []goslack.User) map[string]string {
	const workers = 4
	type result struct {
		id   string
		date string
	}

	jobs := make(chan goslack.User, len(users))
	results := make(chan result, len(users))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				date := ""
				hits, err := d.Client.Search.Messages(ctx, "from:@"+u.Name, 1)
				if err == nil && len(hits) > 0 {
					if t := parseSlackTS(hits[0].Timestamp); !t.IsZero() {
						date = t.UTC().Format("2006-01-02")
					}
				}
				results <- result{id: u.ID, date: date}
			}
		}()
	}
	for _, u := range users {
		jobs <- u
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make(map[string]string, len(users))
	for r := range results {
		out[r.id] = r.date
	}
	return out
}

func parseSlackTS(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	parts := strings.SplitN(ts, ".", 2)
	var sec int64
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return time.Time{}
		}
		sec = sec*10 + int64(c-'0')
	}
	return time.Unix(sec, 0)
}
