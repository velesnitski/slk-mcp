package config

import "testing"

// ADR 112.

func TestLoad_PriorityChannelsForThePrimary(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	t.Setenv("SLACK_PRIORITY_CHANNELS", "#alpha, beta")
	t.Setenv("SLACK_WORKSPACES", "")

	c := Load()
	if len(c.PriorityChannels) != 2 || c.PriorityChannels[0] != "#alpha" || c.PriorityChannels[1] != "beta" {
		t.Fatalf("PriorityChannels = %v", c.PriorityChannels)
	}
	if v := c.WorkspaceViews(); len(v) != 1 || len(v[0].Cfg.PriorityChannels) != 2 {
		t.Fatalf("the primary view must carry its priority channels: %+v", v)
	}
}

func TestLoad_PriorityChannelsPerWorkspaceStayScoped(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xoxp-test")
	t.Setenv("SLACK_PRIORITY_CHANNELS", "alpha")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"second","user_token":"xoxp-test2","priority_channels":"gamma,delta"}]`)

	views := Load().WorkspaceViews()
	if len(views) != 2 {
		t.Fatalf("want 2 views, got %d", len(views))
	}
	if got := views[0].Cfg.PriorityChannels; len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("primary = %v", got)
	}
	if got := views[1].Cfg.PriorityChannels; len(got) != 2 || got[0] != "gamma" {
		t.Fatalf("second workspace must get its own list, got %v", got)
	}
}

func TestParseWorkspaces_PriorityChannelsOptional(t *testing.T) {
	ws, err := ParseWorkspaces(`[{"name":"second","user_token":"xoxp-test2"}]`)
	if err != nil || len(ws) != 1 || ws[0].PriorityChannels != nil {
		t.Fatalf("absent priority_channels must be nil, got %+v err=%v", ws, err)
	}
}
