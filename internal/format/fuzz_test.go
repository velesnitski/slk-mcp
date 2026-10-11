package format

import (
	"encoding/json"
	"testing"

	goslack "github.com/slack-go/slack"
)

// ADR 118: message bodies and Block Kit payloads come from any app in
// the workspace; rendering must hold on all of them.

func FuzzRenderText(f *testing.F) {
	for _, s := range []string{"hi <@U0PEER0001> see <https://example.com|this> in <#C0GENERAL1|general>", "<!subteam^S0GROUP01>", "<", "&lt;&gt;", ""} {
		f.Add(s)
	}
	users := map[string]string{"U0PEER0001": "sam"}
	f.Fuzz(func(t *testing.T, s string) {
		_ = RenderText(s, users)
	})
}

func FuzzRenderBlocks(f *testing.F) {
	f.Add(`[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hi"},{"type":"user","user_id":"U0PEER0001"}]}]}]`)
	f.Add(`[{"type":"rich_text","elements":[{"type":"rich_text_list","style":"bullet","elements":[{"type":"rich_text_section","elements":[{"type":"date","timestamp":1700000000}]}]}]}]`)
	f.Add(`[{"type":"section","text":{"type":"mrkdwn","text":"x"}},{"type":"context","elements":[{"type":"mrkdwn","text":"y"}]}]`)
	f.Add(`[]`)
	f.Fuzz(func(t *testing.T, blocks string) {
		var m goslack.Message
		if json.Unmarshal([]byte(`{"type":"message","ts":"1700000000.000100","blocks":`+blocks+`}`), &m) != nil {
			return
		}
		_ = renderHiddenPayloadMarker(m, 200)
		_ = MessageLine(m, "sam")
	})
}
