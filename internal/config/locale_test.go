package config

import (
	"strings"
	"testing"
)

// ADR 154: SLACK_LOCALE supplies defaults; explicit settings win.
func TestLocale_Defaults(t *testing.T) {
	cases := []struct {
		locale, order, clock string
		wantOrder            string
		want12               bool
	}{
		{"", "", "", "", false},               // nothing set: day-first (empty), 24h
		{"en-US", "", "", "mdy", true},        // US defaults
		{"en_US.UTF-8", "", "", "mdy", true},  // POSIX form accepted
		{"en-GB", "", "", "dmy", false},       // UK
		{"en-AU", "", "", "dmy", true},        // day-first, 12-hour
		{"ru-RU", "", "", "dmy", false},       // Russian
		{"en-US", "dmy", "24h", "dmy", false}, // explicit settings win
		{"", "", "12h", "", true},             // clock alone
	}
	for _, c := range cases {
		t.Setenv("SLACK_USER_TOKEN", "x")
		t.Setenv("SLACK_WORKSPACES", "")
		t.Setenv("SLACK_LOCALE", c.locale)
		t.Setenv("SLACK_DATE_ORDER", c.order)
		t.Setenv("SLACK_CLOCK", c.clock)
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if cfg.DateOrder != c.wantOrder || cfg.Clock12 != c.want12 {
			t.Errorf("%+v: order=%q clock12=%v", c, cfg.DateOrder, cfg.Clock12)
		}
	}
}

func TestLocale_PerWorkspaceAndInvalid(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "x")
	t.Setenv("SLACK_LOCALE", "")
	t.Setenv("SLACK_DATE_ORDER", "")
	t.Setenv("SLACK_CLOCK", "")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"us","user_token":"y","locale":"en-US"},{"name":"eu","user_token":"z","locale":"en-US","date_order":"dmy"}]`)
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	v := cfg.WorkspaceViews()
	if v[1].Cfg.DateOrder != "mdy" || v[2].Cfg.DateOrder != "dmy" {
		t.Errorf("workspace orders = %q, %q; want mdy, dmy", v[1].Cfg.DateOrder, v[2].Cfg.DateOrder)
	}

	for env, val := range map[string]string{"SLACK_LOCALE": "English", "SLACK_CLOCK": "13h"} {
		t.Setenv("SLACK_WORKSPACES", "")
		t.Setenv("SLACK_LOCALE", "")
		t.Setenv("SLACK_CLOCK", "")
		t.Setenv(env, val)
		if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), env) {
			t.Errorf("%s=%q: err = %v", env, val, err)
		}
	}
}
