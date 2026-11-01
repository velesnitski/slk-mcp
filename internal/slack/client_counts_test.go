package slack

import (
	"context"
	"errors"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestParseClientCounts(t *testing.T) {
	got, err := parseClientCounts([]byte(`{"ok":true,
		"channels":[{"id":"C1","has_unreads":true},{"id":"C2","has_unreads":false,"mention_count":1},{"id":"C3"}],
		"mpims":[{"id":"G1","has_unreads":true}],
		"ims":[{"id":"D1","has_unreads":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !got["C1"].HasUnreads || got["C2"].MentionCount != 1 || got["C3"].HasUnreads || !got["G1"].HasUnreads {
		t.Fatalf("got %+v", got)
	}
	if _, ok := got["D1"]; !ok {
		t.Fatal("ims must be read too")
	}
	if _, err := parseClientCounts([]byte(`{"ok":false,"error":"not_allowed_token_type"}`)); err == nil {
		t.Fatal("a refusal must be an error")
	}
	if _, err := parseClientCounts([]byte(`<html>`)); err == nil {
		t.Fatal("garbage must be an error")
	}
}

func TestFilterByCounts(t *testing.T) {
	ch := func(id string) goslack.Channel {
		var c goslack.Channel
		c.ID = id
		return c
	}
	all := []goslack.Channel{ch("C1"), ch("C2"), ch("C3"), ch("C4")}
	s := &UnreadService{log: svLogger()}

	var rep CountsReport
	if got := s.filterByCounts(context.Background(), all, &rep); len(got) != 4 || rep.Used || rep.Err == "" {
		t.Fatalf("no counts source: keep everything, say why: %v %+v", got, rep)
	}

	s.counts = func(context.Context) (map[string]ChannelCount, error) { return nil, errors.New("not_allowed_token_type") }
	rep = CountsReport{}
	if got := s.filterByCounts(context.Background(), all, &rep); len(got) != 4 || rep.Used || rep.Err != "not_allowed_token_type" {
		t.Fatalf("on error: keep everything: %v %+v", got, rep)
	}

	s.counts = func(context.Context) (map[string]ChannelCount, error) {
		return map[string]ChannelCount{"C1": {HasUnreads: true}, "C2": {}, "C3": {MentionCount: 2}}, nil
	}
	rep = CountsReport{}
	got := s.filterByCounts(context.Background(), all, &rep)
	ids := ""
	for _, c := range got {
		ids += c.ID + ","
	}
	if ids != "C1,C3,C4," || !rep.Used || rep.Skipped != 1 || rep.Unlisted != 1 || rep.Channels != 4 {
		t.Fatalf("kept %s report %+v", ids, rep)
	}
	if all[1].ID != "C2" {
		t.Fatal("the caller's slice must not be modified")
	}
}
