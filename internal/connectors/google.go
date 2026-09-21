package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"breaklist/internal/config"
	"breaklist/internal/httpx"
)

// GoogleRedirectURI must match the URI registered on the OAuth client.
const GoogleRedirectURI = "http://localhost:3031/auth/google/callback"

// GoogleScope is read-only calendar access.
const GoogleScope = "https://www.googleapis.com/auth/calendar.readonly"

// CalendarEvent is one upcoming event.
type CalendarEvent struct {
	Title    string
	When     string // formatted for the report
	Start    time.Time
	IsAllDay bool
}

// googleScopeURLs maps the short names stored in config to OAuth scopes.
var googleScopeURLs = map[string]string{
	"calendar": GoogleScope,
	"tasks":    "https://www.googleapis.com/auth/tasks.readonly",
	"keep":     "https://www.googleapis.com/auth/keep.readonly",
}

// GoogleScopes expands the configured short names (calendar by default).
func GoogleScopes(g config.Google) []string {
	names := g.Scopes
	if len(names) == 0 {
		names = []string{"calendar"}
	}
	var out []string
	for _, n := range names {
		if u, ok := googleScopeURLs[strings.ToLower(strings.TrimSpace(n))]; ok {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		out = []string{GoogleScope}
	}
	return out
}

// GoogleAuthURL builds the consent-screen URL for the configured scopes.
func GoogleAuthURL(g config.Google) string {
	return fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&access_type=offline&prompt=consent&include_granted_scopes=true",
		url.QueryEscape(g.ClientID), url.QueryEscape(GoogleRedirectURI), url.QueryEscape(strings.Join(GoogleScopes(g), " ")))
}

// ── Google Tasks ─────────────────────────────────────────────────────────────

// GoogleTask is one to-do from Google Tasks.
type GoogleTask struct {
	Title string
	Notes string
	Due   time.Time
}

// GoogleTaskLists returns the user's task lists (id, title).
func GoogleTaskLists(ctx context.Context, g config.Google) ([]struct{ ID, Title string }, error) {
	token, err := googleAccessToken(ctx, g)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := httpx.GetJSON(ctx, "https://tasks.googleapis.com/tasks/v1/users/@me/lists?maxResults=50", map[string]string{"Authorization": "Bearer " + token}, &resp); err != nil {
		return nil, fmt.Errorf("google tasks: %w (re-link Google with the Tasks scope ticked)", err)
	}
	out := make([]struct{ ID, Title string }, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, struct{ ID, Title string }{it.ID, it.Title})
	}
	return out, nil
}

// GoogleTasks returns open tasks from the list whose title matches listName
// (blank = the default list).
func GoogleTasks(ctx context.Context, g config.Google, listName string, loc *time.Location) ([]GoogleTask, error) {
	lists, err := GoogleTaskLists(ctx, g)
	if err != nil {
		return nil, err
	}
	if len(lists) == 0 {
		return nil, fmt.Errorf("google tasks: no task lists found")
	}
	listID := lists[0].ID
	if listName != "" {
		found := false
		for _, l := range lists {
			if strings.EqualFold(l.Title, listName) {
				listID, found = l.ID, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("google tasks: no list named %q", listName)
		}
	}
	token, err := googleAccessToken(ctx, g)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Title  string `json:"title"`
			Notes  string `json:"notes"`
			Due    string `json:"due"`
			Status string `json:"status"`
		} `json:"items"`
	}
	u := fmt.Sprintf("https://tasks.googleapis.com/tasks/v1/lists/%s/tasks?showCompleted=false&showHidden=false&maxResults=100", url.PathEscape(listID))
	if err := httpx.GetJSON(ctx, u, map[string]string{"Authorization": "Bearer " + token}, &resp); err != nil {
		return nil, fmt.Errorf("google tasks: %w", err)
	}
	var out []GoogleTask
	for _, it := range resp.Items {
		if it.Status == "completed" || strings.TrimSpace(it.Title) == "" {
			continue
		}
		t := GoogleTask{Title: strings.TrimSpace(it.Title), Notes: strings.TrimSpace(it.Notes)}
		if it.Due != "" {
			if d, err := time.Parse(time.RFC3339, it.Due); err == nil {
				t.Due = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// ── Google Keep ──────────────────────────────────────────────────────────────
//
// The Keep API is only enabled for Google Workspace accounts; personal Gmail
// accounts get a permission error, which the module reports plainly.

// KeepNote is a note with either text or checklist items.
type KeepNote struct {
	Title string
	Text  string
	Items []KeepItem
}

// KeepItem is one checklist entry.
type KeepItem struct {
	Text    string
	Checked bool
}

// GoogleKeepNotes lists notes (newest first), excluding trashed ones.
func GoogleKeepNotes(ctx context.Context, g config.Google) ([]KeepNote, error) {
	token, err := googleAccessToken(ctx, g)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Notes []struct {
			Title   string `json:"title"`
			Trashed bool   `json:"trashed"`
			Body    struct {
				Text struct {
					Text string `json:"text"`
				} `json:"text"`
				List struct {
					ListItems []struct {
						Text struct {
							Text string `json:"text"`
						} `json:"text"`
						Checked bool `json:"checked"`
					} `json:"listItems"`
				} `json:"list"`
			} `json:"body"`
		} `json:"notes"`
	}
	if err := httpx.GetJSON(ctx, "https://keep.googleapis.com/v1/notes?pageSize=100&filter=-trashed", map[string]string{"Authorization": "Bearer " + token}, &resp); err != nil {
		return nil, fmt.Errorf("google keep: %w (the Keep API is only available to Google Workspace accounts; re-link Google with the Keep scope ticked)", err)
	}
	var out []KeepNote
	for _, n := range resp.Notes {
		if n.Trashed {
			continue
		}
		note := KeepNote{Title: strings.TrimSpace(n.Title), Text: strings.TrimSpace(n.Body.Text.Text)}
		for _, it := range n.Body.List.ListItems {
			note.Items = append(note.Items, KeepItem{Text: strings.TrimSpace(it.Text.Text), Checked: it.Checked})
		}
		out = append(out, note)
	}
	return out, nil
}

// GoogleExchangeCode swaps an authorization code for a refresh token.
func GoogleExchangeCode(ctx context.Context, g config.Google, code string) (string, error) {
	val := url.Values{}
	val.Set("grant_type", "authorization_code")
	val.Set("code", code)
	val.Set("client_id", g.ClientID)
	val.Set("client_secret", g.ClientSecret)
	val.Set("redirect_uri", GoogleRedirectURI)
	var tok struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	if err := postForm(ctx, "https://oauth2.googleapis.com/token", val, &tok); err != nil {
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token returned; revoke the app at myaccount.google.com and try again")
	}
	return tok.RefreshToken, nil
}

func googleAccessToken(ctx context.Context, g config.Google) (string, error) {
	if g.RefreshToken == "" {
		return "", fmt.Errorf("Google Calendar is not linked yet")
	}
	val := url.Values{}
	val.Set("grant_type", "refresh_token")
	val.Set("refresh_token", g.RefreshToken)
	val.Set("client_id", g.ClientID)
	val.Set("client_secret", g.ClientSecret)
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := postForm(ctx, "https://oauth2.googleapis.com/token", val, &tok); err != nil {
		return "", fmt.Errorf("refreshing google token: %w", err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return "", fmt.Errorf("google token error: %s", tok.Error)
	}
	return tok.AccessToken, nil
}

// GoogleCalendars lists the calendars the linked account can see.
func GoogleCalendars(ctx context.Context, g config.Google) ([]struct{ ID, Summary string }, error) {
	token, err := googleAccessToken(ctx, g)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"items"`
	}
	err = httpx.GetJSON(ctx, "https://www.googleapis.com/calendar/v3/users/me/calendarList", map[string]string{"Authorization": "Bearer " + token}, &resp)
	if err != nil {
		return nil, err
	}
	out := make([]struct{ ID, Summary string }, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, struct{ ID, Summary string }{it.ID, it.Summary})
	}
	return out, nil
}

// GetCalendarEvents returns the next n events, resolving a calendar *name* to
// its id when needed.
func GetCalendarEvents(ctx context.Context, g config.Google, loc *time.Location, n int) ([]CalendarEvent, error) {
	token, err := googleAccessToken(ctx, g)
	if err != nil {
		return nil, err
	}
	auth := map[string]string{"Authorization": "Bearer " + token}
	calendarID := strings.TrimSpace(g.CalendarID)
	if calendarID == "" {
		calendarID = "primary"
	}
	if calendarID != "primary" && !strings.Contains(calendarID, "@") {
		// Treat as a display name and look it up.
		var list struct {
			Items []struct {
				ID      string `json:"id"`
				Summary string `json:"summary"`
			} `json:"items"`
		}
		if err := httpx.GetJSON(ctx, "https://www.googleapis.com/calendar/v3/users/me/calendarList", auth, &list); err == nil {
			for _, it := range list.Items {
				if strings.EqualFold(it.Summary, calendarID) {
					calendarID = it.ID
					break
				}
			}
		}
	}
	timeMin := time.Now().In(loc).Format(time.RFC3339)
	apiURL := fmt.Sprintf("https://www.googleapis.com/calendar/v3/calendars/%s/events?maxResults=%d&orderBy=startTime&singleEvents=true&timeMin=%s",
		url.PathEscape(calendarID), n, url.QueryEscape(timeMin))
	var resp struct {
		Items []struct {
			Summary string `json:"summary"`
			Start   struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
		} `json:"items"`
	}
	if err := httpx.GetJSON(ctx, apiURL, auth, &resp); err != nil {
		return nil, fmt.Errorf("calendar API: %w", err)
	}
	var events []CalendarEvent
	for _, item := range resp.Items {
		ev := CalendarEvent{Title: strings.TrimSpace(item.Summary)}
		if ev.Title == "" {
			ev.Title = "(untitled)"
		}
		switch {
		case item.Start.DateTime != "":
			start, err := time.Parse(time.RFC3339, item.Start.DateTime)
			if err != nil {
				ev.When = item.Start.DateTime
				break
			}
			start = start.In(loc)
			ev.Start = start
			ev.When = start.Format("Mon Jan 2, 3:04 PM")
			if end, err := time.Parse(time.RFC3339, item.End.DateTime); err == nil {
				end = end.In(loc)
				if end.YearDay() != start.YearDay() || end.Year() != start.Year() {
					ev.When = start.Format("Jan 2, 3 PM") + " – " + end.Format("Jan 2, 3 PM")
				}
			}
		case item.Start.Date != "":
			ev.IsAllDay = true
			start, errS := time.ParseInLocation("2006-01-02", item.Start.Date, loc)
			end, errE := time.ParseInLocation("2006-01-02", item.End.Date, loc)
			if errS != nil {
				ev.When = item.Start.Date
				break
			}
			ev.Start = start
			ev.When = start.Format("Mon Jan 2")
			if errE == nil {
				end = end.AddDate(0, 0, -1) // end date is exclusive
				if end.After(start) {
					ev.When = start.Format("Mon Jan 2") + " – " + end.Format("Mon Jan 2")
				}
			}
		}
		events = append(events, ev)
	}
	return events, nil
}

func postForm(ctx context.Context, endpoint string, val url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(val.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", httpx.UserAgent)
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}
