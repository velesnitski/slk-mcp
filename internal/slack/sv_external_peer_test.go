package slack

import (
	"context"
	"net/http"
	"testing"
)

// A person from another organisation (Slack Connect) is not in
// users.list, so their handle never resolved — though the operator has a
// DM with them.

func svExternalPeerFake(t *testing.T) *svFake {
	t.Helper()
	f := svServer(t)
	f.reply("users.list", svJSON{"ok": true, "members": []svJSON{
		{"id": "U0LOCAL001", "name": "sam", "real_name": "Sam Example"},
	}})
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		{"id": "D0DM000001", "is_im": true, "user": "U0LOCAL001"},
		{"id": "D0DM000002", "is_im": true, "user": "U0EXTERN01"},
	}})
	f.on("users.info", func(r *http.Request) svJSON {
		if r.FormValue("user") == "U0EXTERN01" {
			return svJSON{"ok": true, "user": svJSON{"id": "U0EXTERN01", "name": "lee.partner", "real_name": "Lee Partner"}}
		}
		return svJSON{"ok": false, "error": "user_not_found"}
	})
	return f
}

func TestIDForHandle_ExternalDMPeerResolves(t *testing.T) {
	f := svExternalPeerFake(t)
	users := newUserService(svUserAPI(f), svLogger())

	id, err := users.IDForHandle(context.Background(), "@lee.partner")
	if err != nil || id != "U0EXTERN01" {
		t.Fatalf("got %q, %v — want the external DM peer", id, err)
	}
}

func TestIDForHandle_RosterMatchSkipsTheFallback(t *testing.T) {
	f := svExternalPeerFake(t)
	users := newUserService(svUserAPI(f), svLogger())

	if id, err := users.IDForHandle(context.Background(), "sam"); err != nil || id != "U0LOCAL001" {
		t.Fatalf("got %q, %v", id, err)
	}
	if f.count("conversations.list") != 0 {
		t.Fatal("a roster match must not walk the DM list")
	}
}

func TestIDForHandle_UnknownHandleStillFails(t *testing.T) {
	f := svExternalPeerFake(t)
	users := newUserService(svUserAPI(f), svLogger())

	if _, err := users.IDForHandle(context.Background(), "nobody"); err == nil {
		t.Fatal("an unknown handle must still be an error")
	}
}
