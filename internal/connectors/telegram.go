package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tickr/internal/httpx"
)

// Telegram is a minimal client for the parts of the Bot API "send text,
// print it" needs: validate the token, long-poll for new messages, and
// reply. It deliberately does not touch webhooks — long polling means the
// printer only ever makes outbound connections, so no port forwarding or
// public IP is required to receive a message from anywhere.
type Telegram struct {
	token string
}

// NewTelegram validates that a token was supplied (not that it is valid —
// call GetMe for that).
func NewTelegram(token string) (*Telegram, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("Telegram bot token is not set")
	}
	return &Telegram{token: token}, nil
}

// telegramAPIBase is a var (not a const) so tests can point it at a fake
// server; the token and method are appended as api.telegram.org/bot<TOKEN>/<method>.
var telegramAPIBase = "https://api.telegram.org/bot"

func (t *Telegram) api(method string) string {
	return telegramAPIBase + t.token + "/" + method
}

// GetMe validates the token and returns the bot's @username.
func (t *Telegram) GetMe(ctx context.Context) (string, error) {
	var res struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := httpx.GetJSON(ctx, t.api("getMe"), nil, &res); err != nil {
		return "", fmt.Errorf("telegram: %w", err)
	}
	if !res.OK {
		return "", fmt.Errorf("telegram: %s", res.Description)
	}
	return res.Result.Username, nil
}

// TelegramMessage is one incoming text message.
type TelegramMessage struct {
	UpdateID int64
	ChatID   int64
	From     string // sender's first name, used as a friendly note title
	Text     string
	IsText   bool // false for updates with no message text (edits, stickers…)
}

// longPollClient has its own generous timeout: getUpdates is a long-poll
// call that Telegram deliberately holds open for up to `timeoutSec`, which
// would otherwise exceed httpx's shared 20s client timeout.
var longPollClient = &http.Client{Timeout: 40 * time.Second}

// GetUpdates long-polls for messages since offset (pass the previous call's
// highest UpdateID + 1 to avoid re-delivering old messages). Only "message"
// updates are requested, so edits/channel posts/etc. are filtered server-side.
func (t *Telegram) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]TelegramMessage, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	q.Set("allowed_updates", `["message"]`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.api("getUpdates")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	resp, err := longPollClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram: reading response: %w", err)
	}
	var res struct {
		OK     bool `json:"ok"`
		Result []struct {
			UpdateID int64 `json:"update_id"`
			Message  *struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
				From struct {
					FirstName string `json:"first_name"`
				} `json:"from"`
				Text string `json:"text"`
			} `json:"message"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("telegram: decoding response: %w", err)
	}
	if !res.OK {
		return nil, fmt.Errorf("telegram: %s", res.Description)
	}
	out := make([]TelegramMessage, 0, len(res.Result))
	for _, u := range res.Result {
		m := TelegramMessage{UpdateID: u.UpdateID}
		if u.Message != nil {
			m.ChatID = u.Message.Chat.ID
			m.From = u.Message.From.FirstName
			m.Text = u.Message.Text
			m.IsText = strings.TrimSpace(u.Message.Text) != ""
		}
		out = append(out, m)
	}
	return out, nil
}

// SendMessage replies to a chat, e.g. to confirm a note printed.
func (t *Telegram) SendMessage(ctx context.Context, chatID int64, text string) error {
	payload := map[string]any{"chat_id": chatID, "text": text}
	var res struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := httpx.PostJSON(ctx, t.api("sendMessage"), payload, nil, &res); err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("telegram: %s", res.Description)
	}
	return nil
}
