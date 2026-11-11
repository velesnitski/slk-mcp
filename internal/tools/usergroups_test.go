package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const ugRoster = `{"ok":true,"members":[` +
	`{"id":"U01","name":"sam.lee","real_name":"Sam Lee","profile":{"display_name":"Sam","real_name":"Sam Lee"}},` +
	`{"id":"U02","name":"pat","real_name":"Pat Novak","profile":{"real_name":"Pat Novak"}},` +
	`{"id":"U03","name":"kim.o","real_name":"Kim Ortega","profile":{"display_name":"Kim","real_name":"Kim Ortega"}},` +
	`{"id":"U04","name":"kim.r","real_name":"Kim Ross","profile":{"display_name":"Kim","real_name":"Kim Ross"}}` +
	`],"response_metadata":{"next_cursor":""}}`

func ugGroups(body string) string {
	return `{"ok":true,"usergroups":[` + body + `]}`
}

const ugLeads = `{"id":"S01","handle":"leads","name":"Leads","users":["U01","U02"],"user_count":2,"date_delete":0}`

func TestManageUserGroup_CreatesAndSetsMembers(t *testing.T) {
	f := newFakeSlack(t)
	f.On("users.list", ugRoster)
	f.On("usergroups.list", ugGroups(""))
	var created, members, group string
	f.OnFunc("usergroups.create", func(r *http.Request) string {
		created = r.Form.Get("handle") + "|" + r.Form.Get("name")
		return `{"ok":true,"usergroup":{"id":"S09","handle":"tech-leads","name":"tech-leads"}}`
	})
	f.OnFunc("usergroups.users.update", func(r *http.Request) string {
		group, members = r.Form.Get("usergroup"), r.Form.Get("users")
		return `{"ok":true,"usergroup":{"id":"S09"}}`
	})
	h := newFakeHub(t, f)

	res := h.runManageUserGroup(context.Background(), manageGroupArgs{
		handle: "@Tech-Leads", members: "@sam.lee, Pat Novak, Kim Ross", mode: "set",
	})

	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if created != "tech-leads|tech-leads" || group != "S09" || members != "U01,U02,U04" {
		t.Fatalf("create=%q group=%q members=%q", created, group, members)
	}
	tmWant(t, resultText(res), "created @tech-leads", "3 members", "added: Sam Lee, Pat Novak, Kim Ross", "tag them all with @tech-leads")
}

func TestManageUserGroup_AnUnclearMemberChangesNothing(t *testing.T) {
	f := newFakeSlack(t)
	f.On("users.list", ugRoster)
	h := newFakeHub(t, f)

	res := h.runManageUserGroup(context.Background(), manageGroupArgs{handle: "leads", members: "@pat @ghost @Kim"})

	if !res.IsError {
		t.Fatalf("an unknown or shared name must refuse the edit, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "nothing changed", "no such person: @ghost", "@Kim matches more than one person: Kim Ortega, Kim Ross")
	for _, m := range []string{"usergroups.list", "usergroups.create", "usergroups.users.update"} {
		if f.Called(m) {
			t.Fatalf("%s must not be called, calls=%v", m, f.Calls())
		}
	}
}

func TestManageUserGroup_AddAndRemoveEditTheExistingList(t *testing.T) {
	for _, c := range []struct {
		mode, members, want, note string
	}{
		{"add", "kim.o pat", "U01,U02,U03", "added: Kim Ortega"},
		{"remove", "pat", "U01", "removed: Pat Novak"},
		{"set", "U03", "U03", "removed: Sam Lee, Pat Novak"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			f := newFakeSlack(t)
			f.On("users.list", ugRoster)
			f.On("usergroups.list", ugGroups(ugLeads))
			var got string
			f.OnFunc("usergroups.users.update", func(r *http.Request) string {
				got = r.Form.Get("users")
				return `{"ok":true,"usergroup":{"id":"S01"}}`
			})
			h := newFakeHub(t, f)

			res := h.runManageUserGroup(context.Background(), manageGroupArgs{handle: "leads", members: c.members, mode: c.mode})

			if res.IsError || got != c.want {
				t.Fatalf("members sent %q, want %q; result %q", got, c.want, resultText(res))
			}
			tmWant(t, resultText(res), "updated @leads", c.note)
			if f.Called("usergroups.create") {
				t.Fatalf("an existing group must not be re-created")
			}
		})
	}
}

func TestManageUserGroup_GuardsAndDryRun(t *testing.T) {
	f := newFakeSlack(t)
	f.On("users.list", ugRoster)
	f.On("usergroups.list", ugGroups(ugLeads))
	h := newFakeHub(t, f)
	ctx := context.Background()

	// Removing everyone would leave an empty group, which Slack refuses.
	res := h.runManageUserGroup(ctx, manageGroupArgs{handle: "leads", members: "sam.lee pat", mode: "remove"})
	tmWant(t, resultText(res), "cannot be left without members")
	// Same members: no write.
	res = h.runManageUserGroup(ctx, manageGroupArgs{handle: "leads", members: "pat, sam.lee"})
	tmWant(t, resultText(res), "unchanged @leads", "already match")
	// Dry run shows the change and writes nothing.
	res = h.runManageUserGroup(ctx, manageGroupArgs{handle: "new-team", members: "pat", dryRun: true})
	tmWant(t, resultText(res), "would create @new-team", "added: Pat Novak")
	// Bad handle and bad mode fail before any call.
	tmWant(t, resultText(h.runManageUserGroup(ctx, manageGroupArgs{handle: "Bad Handle", members: "pat"})), "use lowercase")
	tmWant(t, resultText(h.runManageUserGroup(ctx, manageGroupArgs{handle: "leads", members: "pat", mode: "replace"})), "use set, add or remove")

	if f.Called("usergroups.users.update") || f.Called("usergroups.create") {
		t.Fatalf("no write expected, calls=%v", f.Calls())
	}
}

func TestManageUserGroup_MissingScopeNamesIt(t *testing.T) {
	f := newFakeSlack(t)
	f.On("users.list", ugRoster)
	f.OnError("usergroups.list", "missing_scope")
	h := newFakeHub(t, f)

	res := h.runManageUserGroup(context.Background(), manageGroupArgs{handle: "leads", members: "pat"})

	if !res.IsError {
		t.Fatalf("want error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "usergroups:read scope", "reinstall")
}

func TestListUserGroups_ShowsMembersByHandle(t *testing.T) {
	f := newFakeSlack(t)
	f.On("users.list", ugRoster)
	f.On("usergroups.list", ugGroups(ugLeads+`,{"id":"S02","handle":"old","name":"Old","users":["U03"],"date_delete":1700000000}`))
	h := newFakeHub(t, f)

	out := resultText(h.runListUserGroups(context.Background(), "", ""))

	tmWant(t, out, "1 user groups", "@leads | Leads | S01 | 2 members: @sam.lee @pat")
	if strings.Contains(out, "@old") {
		t.Fatalf("a disabled group must not be listed: %q", out)
	}
}

// post_message: a word no person answers to is tried as a group handle.
func TestPostMessage_TagsAUserGroup(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("users.list", ugRoster)
	f.On("usergroups.list", ugGroups(ugLeads))
	var gotText string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		gotText = r.Form.Get("text")
		return `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{"channel": "alpha", "text": "@leads meeting at 10, @pat too"}))

	if gotText != "<!subteam^S01> meeting at 10, <@U02> too" {
		t.Fatalf("Slack got %q", gotText)
	}
	tmWant(t, out, "@leads → group, 2 people", "@pat → Pat Novak")
}

func TestPostMessage_GroupsAreNotReadWhenEveryoneResolved(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("users.list", ugRoster)
	f.On("chat.postMessage", `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`)
	s := tmThreadServer(t, newFakeHub(t, f))

	tmCall(t, s, "post_message", map[string]any{"channel": "alpha", "text": "@pat hi"})

	if f.Called("usergroups.list") {
		t.Fatalf("groups are read only for words no person answers to, calls=%v", f.Calls())
	}
}

// When users.list fails, members are still named through users.info.
func TestListUserGroups_RosterFailureFallsBackToLookups(t *testing.T) {
	f := newFakeSlack(t)
	f.OnError("users.list", "fatal_error")
	tmUsers(f, map[string]string{"U01": "sam.lee", "U02": "pat"})
	f.On("usergroups.list", ugGroups(ugLeads))
	h := newFakeHub(t, f)

	out := resultText(h.runListUserGroups(context.Background(), "", ""))

	tmWant(t, out, "@leads | Leads | S01 | 2 members: @sam.lee @pat")
}
