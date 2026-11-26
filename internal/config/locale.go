package config

import (
	"regexp"
	"strings"
)

// Locale defaults (ADR 154). SLACK_LOCALE names a locale ("en-US",
// "en_GB.UTF-8", "ru-RU"); it only supplies defaults for the date order
// and the clock, and SLACK_DATE_ORDER / SLACK_CLOCK override each.
// The machine's LANG is deliberately not read: it is often en_US on
// machines whose owners write 27.11 and 15:04, and a silent switch to
// US formats would misread their deadlines.

var localeRe = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z]{2})?$`)

// monthFirstLocales write 12/3 for December 3.
var monthFirstLocales = map[string]bool{"en-us": true, "es-us": true, "en-ph": true}

// clock12Locales commonly write 3:04 PM.
var clock12Locales = map[string]bool{
	"en-us": true, "es-us": true, "en-ph": true, "en-ca": true, "en-au": true,
	"en-nz": true, "en-in": true, "hi-in": true,
}

// NormalizeLocale turns "en_US.UTF-8" or "EN-us" into "en-us"; ok is
// false for anything that is not a language with an optional region.
func NormalizeLocale(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(strings.ReplaceAll(s, "_", "-"))
	if s == "" {
		return "", true
	}
	return s, localeRe.MatchString(s)
}

// localeDateOrder is the date order a locale implies: "mdy" or "dmy".
func localeDateOrder(loc string) string {
	if monthFirstLocales[loc] {
		return "mdy"
	}
	return "dmy"
}

// parseClock reads SLACK_CLOCK: "12h" or "24h" (also "12"/"24").
func parseClock(s string) (twelve bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "12h", "12":
		return true, true
	case "24h", "24", "":
		return false, true
	}
	return false, false
}

// applyLocale fills DateOrder and Clock12 from the locale where they are
// not set explicitly, for the config and every workspace.
func (c *Config) applyLocale(clockEnv string) {
	loc, _ := NormalizeLocale(c.Locale)
	if c.DateOrder == "" && loc != "" {
		c.DateOrder = localeDateOrder(loc)
	}
	if strings.TrimSpace(clockEnv) != "" {
		c.Clock12, _ = parseClock(clockEnv)
	} else {
		c.Clock12 = clock12Locales[loc]
	}
	for i := range c.Workspaces {
		ws := &c.Workspaces[i]
		if ws.DateOrder != "" {
			continue
		}
		if wl, _ := NormalizeLocale(ws.Locale); wl != "" {
			ws.DateOrder = localeDateOrder(wl)
		}
	}
}
