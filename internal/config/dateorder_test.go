package config

import (
	"strings"
	"testing"
)

// ADR 152: SLACK_DATE_ORDER and the per-workspace date_order.
func TestDateOrder_LoadAndValidate(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "x")
	t.Setenv("SLACK_DATE_ORDER", "mdy")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"beta","user_token":"y","date_order":"dmy"}]`)
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	views := c.WorkspaceViews()
	if views[0].Cfg.DateOrder != "mdy" || views[1].Cfg.DateOrder != "dmy" {
		t.Errorf("views = %q, %q; want mdy, dmy", views[0].Cfg.DateOrder, views[1].Cfg.DateOrder)
	}

	t.Setenv("SLACK_DATE_ORDER", "ymd")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "SLACK_DATE_ORDER") {
		t.Errorf("bad SLACK_DATE_ORDER: err = %v", err)
	}
	t.Setenv("SLACK_DATE_ORDER", "")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"beta","user_token":"y","date_order":"weird"}]`)
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "date_order") {
		t.Errorf("bad date_order: err = %v", err)
	}
}
