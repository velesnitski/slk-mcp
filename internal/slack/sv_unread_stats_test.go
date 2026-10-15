package slack

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ADR 122: UnreadAll reports where its time goes.

func TestUnreadAll_LogsOneStatsLinePerSweep(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil), svChan("C3", "gamma", nil),
	}})
	f.on("conversations.info", func(r *http.Request) svJSON {
		switch r.FormValue("channel") {
		case "C1":
			return svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{"last_read": "1700099000.000000"})}
		case "C3":
			return svJSON{"ok": false, "error": "channel_not_found"}
		}
		return svJSON{"ok": true, "channel": svChan("C2", "beta", nil)} // never read: no history call
	})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099500.000000", "U2", "hello", nil),
	}})

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	api := svUserAPI(f)
	users := newUserService(api, log)
	svc := newUnreadService(api, newChannelService(api, users, log), users, nil, log)

	if _, err := svc.UnreadAll(context.Background(), 10); err != nil {
		t.Fatalf("UnreadAll: %v", err)
	}
	line := ""
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, `msg="unread sweep"`) {
			line = l
		}
	}
	for _, want := range []string{"channels=3", "history_reads=1", "with_unread=1", "failed=1", "list_took=", "slowest_channel=C"} {
		if !strings.Contains(line, want) {
			t.Fatalf("stats line missing %q: %q", want, line)
		}
	}
}

func TestUnreadStats_Tally(t *testing.T) {
	var u unreadStats
	u.add("C1", time.Second, &ChannelUnread{LastRead: "1.0"}, nil)
	u.add("C2", 3*time.Second, &ChannelUnread{}, nil)
	u.add("C3", 2*time.Second, nil, errors.New("x"))
	if u.historyReads != 1 || u.failed != 1 || u.slowestID != "C2" || u.slowest != 3*time.Second {
		t.Fatalf("got %+v", u)
	}
}
