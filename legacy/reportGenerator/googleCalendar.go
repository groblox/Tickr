package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"shared"
	"strings"
	"time"
)

// CalendarEvent holds a single upcoming Google Calendar event for the template.
type CalendarEvent struct {
	Title    string
	DateTime string // e.g. "Thu Jun 5, 2:30 PM"
	IsAllDay bool
}

// ── OAuth2 helpers ─────────────────────────────────────────────────────────────

func googleClientID() string     { return os.Getenv("GOOGLE_CLIENT_ID") }
func googleClientSecret() string { return os.Getenv("GOOGLE_CLIENT_SECRET") }

const googleRedirectURI = "http://localhost:3031/auth/google/callback"
const googleScope = "https://www.googleapis.com/auth/calendar.readonly"

// googleAccessToken exchanges the stored refresh token for a fresh access token.
func googleAccessToken() (string, error) {
	refreshToken := os.Getenv("GOOGLE_REFRESH_TOKEN")
	if refreshToken == "" {
		return "", fmt.Errorf("GOOGLE_REFRESH_TOKEN not set — run with --auth-google first")
	}

	val := url.Values{}
	val.Set("grant_type", "refresh_token")
	val.Set("refresh_token", refreshToken)
	val.Set("client_id", googleClientID())
	val.Set("client_secret", googleClientSecret())

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", val)
	if err != nil {
		return "", fmt.Errorf("refreshing google token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google token refresh failed (%d): %s", resp.StatusCode, body)
	}

	var res struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	if res.Error != "" {
		return "", fmt.Errorf("google token error: %s", res.Error)
	}
	return res.AccessToken, nil
}

// ── Fetch events ───────────────────────────────────────────────────────────────

// getCalendarEvents returns the next n upcoming events from the user's primary
// Google Calendar (or the calendar specified by GOOGLE_CALENDAR_ID).
func getCalendarEvents(n int) ([]CalendarEvent, error) {
	accessToken, err := googleAccessToken()
	if err != nil {
		return nil, err
	}

	calendarID := os.Getenv("GOOGLE_CALENDAR_ID")
	if calendarID == "" {
		calendarID = "primary"
	}

	loc, _ := time.LoadLocation(os.Getenv("TIMEZONE"))
	if loc == nil {
		loc = time.Local
	}

	timeMin := time.Now().In(loc).Format(time.RFC3339)
	apiURL := fmt.Sprintf(
		"https://www.googleapis.com/calendar/v3/calendars/%s/events?maxResults=%d&orderBy=startTime&singleEvents=true&timeMin=%s",
		url.PathEscape(calendarID), n, url.QueryEscape(timeMin),
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating calendar request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching calendar events: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calendar API error (%d): %s", res.StatusCode, raw)
	}

	var response struct {
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
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("parsing calendar response: %w", err)
	}

	var events []CalendarEvent
	for _, item := range response.Items {
		ev := CalendarEvent{Title: strings.TrimSpace(item.Summary)}

		if item.Start.DateTime != "" {
			// Timed event
			tStart, err := time.Parse(time.RFC3339, item.Start.DateTime)
			if err != nil {
				ev.DateTime = item.Start.DateTime
			} else {
				tStart = tStart.In(loc)
				if item.End.DateTime != "" {
					tEnd, err := time.Parse(time.RFC3339, item.End.DateTime)
					if err == nil {
						tEnd = tEnd.In(loc)
						// Multi-day timed event
						if tEnd.Day() != tStart.Day() || tEnd.Month() != tStart.Month() {
							ev.DateTime = fmt.Sprintf("%s - %s",
								tStart.Format("Jan 2, 3 PM"),
								tEnd.Format("Jan 2, 3 PM"))
						} else {
							ev.DateTime = tStart.Format("Mon Jan 2, 3:04 PM")
						}
					} else {
						ev.DateTime = tStart.Format("Mon Jan 2, 3:04 PM")
					}
				} else {
					ev.DateTime = tStart.Format("Mon Jan 2, 3:04 PM")
				}
			}
		} else if item.Start.Date != "" {
			// All-day event — end.date is exclusive, so subtract 1 day
			tStart, errS := time.Parse("2006-01-02", item.Start.Date)
			tEnd, errE := time.Parse("2006-01-02", item.End.Date)
			if errS == nil && errE == nil {
				tEnd = tEnd.AddDate(0, 0, -1) // make inclusive
				if tEnd.Equal(tStart) {
					// Single day
					ev.DateTime = tStart.Format("Mon Jan 2")
					ev.IsAllDay = true
				} else {
					// Multi-day range
					ev.DateTime = fmt.Sprintf("%s - %s",
						tStart.Format("Mon Jan 2"),
						tEnd.Format("Mon Jan 2"))
					ev.IsAllDay = true
				}
			} else if errS == nil {
				ev.DateTime = tStart.Format("Mon Jan 2")
				ev.IsAllDay = true
			} else {
				ev.DateTime = item.Start.Date
				ev.IsAllDay = true
			}
		}

		events = append(events, ev)
	}

	return events, nil
}

// ── One-time auth CLI ──────────────────────────────────────────────────────────

// runGoogleAuth starts a local OAuth2 callback server, opens the browser to
// authorize, then stores the resulting refresh token in .env.
func runGoogleAuth() {
	fmt.Println("==================================================")
	fmt.Println("   Breaklist — Google Calendar Authentication")
	fmt.Println("==================================================")

	clientID := googleClientID()
	clientSecret := googleClientSecret()
	if clientID == "" || clientSecret == "" {
		fmt.Println("Error: GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set in .env")
		return
	}

	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&access_type=offline&prompt=consent",
		url.QueryEscape(clientID),
		url.QueryEscape(googleRedirectURI),
		url.QueryEscape(googleScope),
	)

	fmt.Println("\nOpening your browser for Google Calendar authorization...")
	fmt.Println("If it doesn't open, visit this URL manually:")
	fmt.Println(authURL)
	fmt.Println("\nWaiting for browser redirect...")
	openBrowser(authURL)

	srv := &http.Server{Addr: ":3031"}

	http.HandleFunc("/auth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			fmt.Fprintln(w, "Error: missing authorization code.")
			fmt.Println("Redirect received but missing code.")
			return
		}

		// Exchange code for tokens
		val := url.Values{}
		val.Set("grant_type", "authorization_code")
		val.Set("code", code)
		val.Set("client_id", clientID)
		val.Set("client_secret", clientSecret)
		val.Set("redirect_uri", googleRedirectURI)

		resp, err := http.PostForm("https://oauth2.googleapis.com/token", val)
		if err != nil {
			msg := fmt.Sprintf("Error exchanging code: %v", err)
			fmt.Fprintln(w, msg)
			fmt.Println(msg)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			msg := fmt.Sprintf("Token exchange failed (%s): %s", resp.Status, body)
			fmt.Fprintln(w, msg)
			fmt.Println(msg)
			return
		}

		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			fmt.Fprintln(w, "Error decoding token response.")
			return
		}
		if tokenResp.RefreshToken == "" {
			fmt.Fprintln(w, "Error: no refresh token returned. Try revoking app access at myaccount.google.com and re-authorizing.")
			fmt.Println("No refresh token in response.")
			return
		}

		// Save to both .env locations
		_ = shared.UpdateEnvFile(".env", "GOOGLE_REFRESH_TOKEN", tokenResp.RefreshToken)
		_ = shared.UpdateEnvFile("../.env", "GOOGLE_REFRESH_TOKEN", tokenResp.RefreshToken)
		os.Setenv("GOOGLE_REFRESH_TOKEN", tokenResp.RefreshToken)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Google Calendar Linked!</title>
<style>
  body { font-family: sans-serif; text-align: center; margin-top: 100px; background: #f7f9fa; }
  .card { background: white; padding: 40px; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.08); display: inline-block; }
  h1 { color: #4285F4; }
</style>
</head>
<body>
<div class="card">
  <h1>&#10003; Google Calendar Linked!</h1>
  <p>Authorization successful. You can close this tab.</p>
</div>
</body>
</html>`)

		fmt.Println("\n✓ Google Calendar authorized! Refresh token saved to .env")
		fmt.Println("Shutting down authentication server...")
		go func() { _ = srv.Shutdown(context.Background()) }()
	})

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP server error: %v\n", err)
	}
}
