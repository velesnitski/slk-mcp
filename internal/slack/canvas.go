package slack

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// CanvasService resolves Slack canvases — the file-backed documents
// attached to channels (the canvas tab) or shared into them as files.
// It holds BOTH identities: a channel canvas is exposed on
// conversations.info `properties.canvas`, but visibility differs by
// token (a bot that isn't a full member may see no properties at all),
// so lookups try the primary identity first and fall back to the user
// client when the two are distinct.
//
// It also carries the raw user token and an HTTP client: the
// workspace-wide canvas delta (RecentCanvases) has to bypass slack-go
// because the SDK drops files.list's `updated` field.
type CanvasService struct {
	primary *goslack.Client
	user    *goslack.Client // may be nil, may equal primary
	token   string          // user token for the raw files.list path
	http    *http.Client
	log     *slog.Logger
	BaseURL string // override for tests; defaults to defaultFilesAPIBase
}

func newCanvasService(primary, user *goslack.Client, userToken string, log *slog.Logger) *CanvasService {
	return &CanvasService{
		primary: primary,
		user:    user,
		token:   userToken,
		http:    &http.Client{Timeout: 30 * time.Second},
		log:     log,
		BaseURL: defaultFilesAPIBase,
	}
}

// clients returns the distinct API identities to try, primary first.
func (s *CanvasService) clients() []*goslack.Client {
	out := []*goslack.Client{s.primary}
	if s.user != nil && s.user != s.primary {
		out = append(out, s.user)
	}
	return out
}

// ChannelCanvas returns the file ID of the canvas attached to a channel
// (the canvas tab) and whether it is empty. ("", false, nil) means the
// channel simply has no attached canvas — not an error.
func (s *CanvasService) ChannelCanvas(ctx context.Context, channelID string) (fileID string, isEmpty bool, err error) {
	var lastErr error
	for _, api := range s.clients() {
		var info *goslack.Channel
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			r, err := api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{ChannelID: channelID})
			if err != nil {
				return err
			}
			info = r
			return nil
		})
		if err != nil {
			lastErr = err
			continue
		}
		if info.Properties != nil && info.Properties.Canvas.FileId != "" {
			return info.Properties.Canvas.FileId, info.Properties.Canvas.IsEmpty, nil
		}
	}
	if lastErr != nil {
		return "", false, fmt.Errorf("conversations.info: %w", lastErr)
	}
	return "", false, nil
}

// CanvasFiles lists canvas-type files shared in (or attached to) a
// channel, newest first as returned by files.list. Tries the user
// identity first — canvases follow user visibility — then the primary.
// An empty list with nil error means "none found".
func (s *CanvasService) CanvasFiles(ctx context.Context, channelID string) ([]goslack.File, error) {
	// User first: canvas files ride user-level visibility.
	apis := s.clients()
	if len(apis) == 2 {
		apis[0], apis[1] = apis[1], apis[0]
	}
	var lastErr error
	for _, api := range apis {
		var files []goslack.File
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			fs, _, err := api.GetFilesContext(ctx, goslack.GetFilesParameters{
				Channel: channelID,
				Types:   "canvas",
				Count:   20,
			})
			if err != nil {
				return err
			}
			files = fs
			return nil
		})
		if err != nil {
			lastErr = err
			continue
		}
		if len(files) > 0 {
			return files, nil
		}
	}
	return nil, lastErr
}
