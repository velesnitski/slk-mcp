package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrCanvasEditNoToken is returned when a canvas edit is attempted
// without a user token. Edits run as the operator, never as the bot:
// canvas access follows people, and an edit should show up in the
// canvas history under the name of whoever asked for it. See ADR 124.
var ErrCanvasEditNoToken = errors.New("slack-canvas: editing a canvas needs a SLACK_USER_TOKEN with the canvases:write scope")

// CanvasEdit is one change in a canvases.edit call. Markdown fills
// document_content; Title fills title_content and is used only by
// "rename" — slack-go's CanvasChange has no title field, which is why
// this path is raw HTTP like RecentCanvases.
type CanvasEdit struct {
	Operation string
	SectionID string
	Markdown  string
	Title     string
}

type canvasContent struct {
	Type     string `json:"type"`
	Markdown string `json:"markdown"`
}

type canvasChangeJSON struct {
	Operation       string         `json:"operation"`
	SectionID       string         `json:"section_id,omitempty"`
	DocumentContent *canvasContent `json:"document_content,omitempty"`
	TitleContent    *canvasContent `json:"title_content,omitempty"`
}

// encodeCanvasChanges builds the `changes` JSON canvases.edit expects.
// "delete" carries no content at all; "rename" carries only the title.
// Pure.
func encodeCanvasChanges(edits []CanvasEdit) (string, error) {
	out := make([]canvasChangeJSON, 0, len(edits))
	for _, e := range edits {
		c := canvasChangeJSON{Operation: e.Operation, SectionID: e.SectionID}
		switch e.Operation {
		case "delete":
		case "rename":
			c.TitleContent = &canvasContent{Type: "markdown", Markdown: e.Title}
		default:
			c.DocumentContent = &canvasContent{Type: "markdown", Markdown: e.Markdown}
		}
		out = append(out, c)
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// EditCanvas applies changes to a canvas via canvases.edit as the
// operator. Rate limits are retried: a 429 means Slack applied nothing.
func (s *CanvasService) EditCanvas(ctx context.Context, canvasID string, edits []CanvasEdit) error {
	changes, err := encodeCanvasChanges(edits)
	if err != nil {
		return fmt.Errorf("slack-canvas: encode changes: %w", err)
	}
	form := url.Values{"canvas_id": {canvasID}, "changes": {changes}}
	_, err = s.callUser(ctx, "canvases.edit", form)
	return err
}

// LookupCanvasSections returns the IDs of the canvas sections whose text
// contains `contains`, optionally narrowed to section types (h1, h2, h3,
// any_header). Slack matches the text itself; the order is the
// document's.
func (s *CanvasService) LookupCanvasSections(ctx context.Context, canvasID, contains string, types []string) ([]string, error) {
	criteria := map[string]any{"contains_text": contains}
	if len(types) > 0 {
		criteria["section_types"] = types
	}
	b, err := json.Marshal(criteria)
	if err != nil {
		return nil, fmt.Errorf("slack-canvas: encode criteria: %w", err)
	}
	raw, err := s.callUser(ctx, "canvases.sections.lookup", url.Values{"canvas_id": {canvasID}, "criteria": {string(b)}})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Sections []struct {
			ID string `json:"id"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("slack-canvas: parse sections: %w", err)
	}
	ids := make([]string, 0, len(resp.Sections))
	for _, sec := range resp.Sections {
		ids = append(ids, sec.ID)
	}
	return ids, nil
}

// callUser posts one Web API method with the user token and returns the
// body of an ok response. A Slack error comes back with the detail lines
// Slack adds under response_metadata.messages, which is where
// canvases.edit explains a rejected markdown.
func (s *CanvasService) callUser(ctx context.Context, method string, form url.Values) ([]byte, error) {
	if s.token == "" {
		return nil, ErrCanvasEditNoToken
	}
	var raw []byte
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		endpoint := strings.TrimRight(s.BaseURL, "/") + "/" + method
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return fmt.Errorf("slack-canvas: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := s.http.Do(req)
		if err != nil {
			return fmt.Errorf("slack-canvas: http: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return &goslack.RateLimitedError{RetryAfter: parseRetryAfterSeconds(resp.Header.Get("Retry-After"))}
		}
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("slack-canvas: read body: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return raw, slackResponseErr(method, raw)
}

// slackResponseErr turns a Slack envelope into an error, or nil when ok.
// Pure.
func slackResponseErr(method string, raw []byte) error {
	var env struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Metadata struct {
			Messages []string `json:"messages"`
		} `json:"response_metadata"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("slack-canvas: %s: parse response: %w (body=%q)", method, err, truncateRaw(raw, 200))
	}
	if env.OK {
		return nil
	}
	msg := env.Error
	if msg == "" {
		msg = "unknown_error"
	}
	if env.Needed != "" {
		msg += " (needs scope " + env.Needed + ")"
	}
	if len(env.Metadata.Messages) > 0 {
		msg += ": " + strings.Join(env.Metadata.Messages, "; ")
	}
	return fmt.Errorf("slack-canvas: %s: %s", method, msg)
}
