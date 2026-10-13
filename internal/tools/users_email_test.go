package tools

import (
	"context"
	"strings"
	"testing"
)

// ADR 120: list_users with_email / email_domain.

const (
	ueRu     = `{"id":"U1","name":"anna","real_name":"Anna","updated":1700000000,"profile":{"real_name":"Anna","email":"anna@mail.example.ru"}}`
	ueNet    = `{"id":"U2","name":"ben","real_name":"Ben","updated":1700000000,"profile":{"real_name":"Ben","email":"ben@example.net"}}`
	ueGuru   = `{"id":"U3","name":"cy","real_name":"Cy","updated":1700000000,"profile":{"real_name":"Cy","email":"cy@guru.com"}}`
	ueHidden = `{"id":"U4","name":"dee","real_name":"Dee","updated":1700000000,"profile":{"real_name":"Dee"}}`
)

func TestListUsers_WithEmailAddsTheColumnAndCoverage(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(ueRu, ueNet, ueHidden))
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", false, userRowOpts{withEmail: true}, ""))

	for _, want := range []string{
		"U1 | anna | Anna |  | | profile_updated=2023-11-14 | email=anna@mail.example.ru",
		"email=ben@example.net",
		"U4 | dee | Dee |  | | profile_updated=2023-11-14 | email=(not visible)",
		"emails visible: 2 of 3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestListUsers_EmailDomainKeepsOnlyThatDomainAndItsSubdomains(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(ueRu, ueNet, ueGuru, ueHidden))
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", false, userRowOpts{withEmail: true, emailDomain: "ru"}, ""))

	if !strings.HasPrefix(text, "1 users\n") || !strings.Contains(text, "anna@mail.example.ru") {
		t.Fatalf("want only the .ru user; got:\n%s", text)
	}
	for _, leak := range []string{"ben@", "guru.com", "dee"} {
		if strings.Contains(text, leak) {
			t.Errorf("%q must be filtered out:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, "emails visible: 3 of 4") || !strings.Contains(text, "filtered to @ru") {
		t.Errorf("coverage must describe the scanned roster, not the filtered one:\n%s", text)
	}
}

func TestListUsers_NoVisibleEmailsSaysTheScopeIsMissing(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex, usMemberSam))
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", false, userRowOpts{withEmail: true, emailDomain: "ru"}, ""))

	if !strings.HasPrefix(text, "0 users\n") {
		t.Fatalf("got:\n%s", text)
	}
	if !strings.Contains(text, "users:read.email") || !strings.Contains(text, "No domain check was possible") {
		t.Errorf("an empty match without emails must name the missing scope:\n%s", text)
	}
}

func TestListUsers_WithoutEmailOptionTheRowIsUnchanged(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(ueRu))
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", false, userRowOpts{}, ""))

	if strings.Contains(text, "email") {
		t.Errorf("emails must stay out unless asked for:\n%s", text)
	}
}

func TestEmailOnDomain(t *testing.T) {
	for _, tc := range []struct {
		email, domain string
		want          bool
	}{
		{"a@mail.ru", "ru", true},
		{"a@MAIL.RU", "ru", true},
		{"a@x.example.ru", "example.ru", true},
		{"a@example.ru", "example.ru", true},
		{"a@guru.com", "ru", false},
		{"a@ru.example.com", "ru", false},
		{"a@example.net", "ru", false},
		{"not-an-email", "ru", false},
		{"a@mail.ru", "", false},
		{"", "ru", false},
	} {
		if got := emailOnDomain(tc.email, tc.domain); got != tc.want {
			t.Errorf("emailOnDomain(%q, %q) = %v, want %v", tc.email, tc.domain, got, tc.want)
		}
	}
}

func TestNormalizeEmailDomain(t *testing.T) {
	for in, want := range map[string]string{"ru": "ru", " .RU ": "ru", "@Mail.Ru": "mail.ru", "": "", "@.ru": "ru"} {
		if got := normalizeEmailDomain(in); got != want {
			t.Errorf("normalizeEmailDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailCoverageNote(t *testing.T) {
	if got := emailCoverageNote(0, 0, ""); got != "emails visible: 0 of 0" {
		t.Errorf("empty roster: %q", got)
	}
	if got := emailCoverageNote(5, 5, ""); got != "emails visible: 5 of 5" {
		t.Errorf("full coverage: %q", got)
	}
	if got := emailCoverageNote(4, 5, ""); !strings.Contains(got, "4 of 5") || !strings.Contains(got, "bots and some guests") {
		t.Errorf("partial coverage: %q", got)
	}
	if got := emailCoverageNote(0, 3, "ru"); !strings.Contains(got, "users:read.email") {
		t.Errorf("no coverage: %q", got)
	}
}

func TestListUsers_ToolArgumentsReachTheRoster(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(ueRu, ueNet))
	s := tmServer(t, f.hub(t).registerUserTools)

	out := resultText(tmCall(t, s, "list_users", map[string]any{"email_domain": "@RU"}))

	if !strings.Contains(out, "anna@mail.example.ru") || strings.Contains(out, "ben@") {
		t.Fatalf("email_domain must imply with_email and filter:\n%s", out)
	}
	out = resultText(tmCall(t, s, "list_users", map[string]any{"with_email": true}))
	if !strings.Contains(out, "ben@example.net") {
		t.Fatalf("with_email must add the column:\n%s", out)
	}
}
