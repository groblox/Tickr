package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTelegram mimics the three Bot API methods Tickr uses, keyed off the
// token embedded in the URL path the way the real API works.
func fakeTelegram(t *testing.T, validToken string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/bot"+validToken+"/") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"tickr_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			off := r.URL.Query().Get("offset")
			if off == "0" || off == "" {
				_, _ = w.Write([]byte(`{"ok":true,"result":[
					{"update_id":101,"message":{"chat":{"id":555},"from":{"first_name":"Ben"},"text":"pick up milk"}},
					{"update_id":102,"message":{"chat":{"id":999},"from":{"first_name":"Stranger"},"text":"hello"}},
					{"update_id":103,"message":{"chat":{"id":555},"from":{"first_name":"Ben"}}}
				]}`))
			} else {
				_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			}
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["chat_id"] == nil || body["text"] == nil {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(mux)
}

func TestTelegramClient(t *testing.T) {
	srv := fakeTelegram(t, "good-token")
	defer srv.Close()
	origAPI := telegramAPIBase
	telegramAPIBase = srv.URL + "/bot"
	defer func() { telegramAPIBase = origAPI }()

	ctx := context.Background()
	tg, err := NewTelegram("good-token")
	if err != nil {
		t.Fatal(err)
	}
	if username, err := tg.GetMe(ctx); err != nil || username != "tickr_bot" {
		t.Fatalf("GetMe: %v %q", err, username)
	}

	msgs, err := tg.GetUpdates(ctx, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(msgs))
	}
	if msgs[0].ChatID != 555 || msgs[0].From != "Ben" || msgs[0].Text != "pick up milk" || !msgs[0].IsText {
		t.Fatalf("message 0 wrong: %+v", msgs[0])
	}
	if msgs[1].ChatID != 999 {
		t.Fatalf("message 1 wrong: %+v", msgs[1])
	}
	if msgs[2].IsText {
		t.Fatalf("message 2 (no text) should not be IsText: %+v", msgs[2])
	}

	// Next page with a real offset returns nothing (simulating "caught up").
	msgs, err = tg.GetUpdates(ctx, 104, 1)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("expected empty page, got %v %v", msgs, err)
	}

	if err := tg.SendMessage(ctx, 555, "🖨️ Printed."); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	bad, _ := NewTelegram("wrong-token")
	if _, err := bad.GetMe(ctx); err == nil {
		t.Fatal("bad token should fail")
	}
	if _, err := NewTelegram("  "); err == nil {
		t.Fatal("blank token should fail")
	}
}
