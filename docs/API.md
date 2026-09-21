# HTTP API

The GUI talks to the server through this JSON API. It listens on `0.0.0.0:<port>` (default 8787), so it is reachable from other devices on the LAN, and it has no authentication. Anyone who can reach it can change settings, read connector status (secrets are redacted), and trigger prints — only run this on a network you trust.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/catalog` | Every module: id, name, description, category, option schema, required connectors. |
| GET | `/api/config` | The configuration with secrets replaced by `********`. |
| PUT | `/api/config` | Save a configuration. Fields still equal to `********` keep their stored value. Returns the redacted result. |
| POST | `/api/layout/reset` | Restore the default section order and flags. |
| GET | `/api/status` | Version, data folder, whether a run is in progress, last result, next scheduled run, run history, connector readiness. |
| POST | `/api/generate` | Body `{"only": ["id", …], "print": bool}` (both optional). Builds the report synchronously and returns `{result, print}`. |
| POST | `/api/preview/{id}` | Body `{"options": {...}, "config": {...}}`. Renders one module with unsaved options and returns `{html}` or `{empty: true}` or `{error}`. |
| POST | `/api/print` | Runs the print command on the last PDF. |
| POST | `/api/test/weather` | Body `{"location": "lat,lng", "weather": {...}}`. Fetches a forecast with the given provider. |
| POST | `/api/test/homeassistant` | Body `{"url", "token"}`. Pings the instance and counts entities. |
| GET | `/api/ha/entities` | Lists Home Assistant entities (id, friendly name, state, unit) for the picker. |
| POST | `/api/test/aeris` | Body `{"clientId", "clientSecret", "stationId"}`. Reads the station. |
| GET | `/api/google/calendars` | Lists calendars visible to the linked Google account. |
| POST | `/api/test/ai` | Body `{"provider": "anthropic"\|"openai", "ai": {...}}`. Sends a one-word prompt to confirm the key works. |
| POST | `/api/test/grafana` | Body `{"url", "token"}`. Checks the server and lists data sources with their UIDs. |
| GET | `/api/grafana/datasources` | Lists Grafana data sources using the saved connector. |
| GET | `/api/log?n=300` | Last n lines of `output/breaklist.log`. |
| POST | `/api/schedules/{index}/run` | Runs a schedule now (generate, and print if the schedule prints), with its retry policy. |
| POST | `/api/auth/google/start` | Saves the client id/secret from the body, starts the callback listener on :3031 and returns `{url}` to open. |
| POST | `/api/auth/dropbox/start` | Same for Dropbox on :3030 (PKCE). |
| POST | `/api/disconnect/{google\|dropbox\|homeassistant}` | Forgets the stored token. |
| GET | `/output/breaklist.pdf` | The latest report. `/output/breaklist.html` is the HTML it was rendered from. |
| POST | `/api/print-text` | Body `{"title", "text"}`. Prints just that text, not a full report. Same trust boundary as `/api/config` (localhost, no token) — used by the GUI's "Send a note" box. |
| POST | `/api/test/telegram` | Body `{"botToken", ...}` (falls back to the saved token if blank). Confirms the bot token and returns its `@username`. |
| POST | `/api/messaging/token` | Generates and saves a new bearer token for the print-text API, returning `{"token": "..."}` once — it is not stored in plaintext anywhere retrievable afterward, so copy it immediately. |

Errors are returned as `{"error": "message"}` with a 4xx/5xx status.

## Text-to-print

Two independent channels print arbitrary text — not a full report — on demand:

- **Telegram.** Set a bot token (create one via [@BotFather](https://t.me/BotFather)) and a list of allowed chat ids in the Connectors tab, or in `connectors.telegram`. The server long-polls Telegram outbound, so nothing needs to be exposed to the internet. Message the bot from an unlisted chat and it replies with that chat's id to add to the allow list; once allowed, any text message prints immediately.
- **Generic HTTP.** Generating a token in the Connectors tab (or via `POST /api/messaging/token`) starts a second, minimal HTTP listener on `server.printPort` (default 8788, bound to all interfaces) that serves only `POST /print-text` and `GET /healthz`. Every `/print-text` request must carry `Authorization: Bearer <token>`; it is otherwise isolated from the main GUI listener, which stays 127.0.0.1-only and unauthenticated. The listener starts only once a token exists and stops the moment the token is cleared.

```
curl -X POST http://<host>:8788/print-text \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"title":"Note","text":"pick up milk on the way home"}'
```

Both channels funnel through the same renderer and print command as a full report, and every attempt (source, text, success/failure) is recorded and shown in the GUI's "Notes printed" history.

## Configuration document

```jsonc
{
  "version": 2,
  "general": { "timezone": "America/Chicago", "location": "33.40,-86.81", "locationName": "Bluff Park",
               "units": "imperial", "childName": "", "paperWidthMm": 47, "marginBottomMm": 7,
               "wkhtmltopdf": "", "dateFormat": "Mon Jan 2, 2006" },
  "connectors": {
    "weather": { "provider": "openmeteo", "tomorrowApiKey": "" },
    "aeris": { "clientId": "", "clientSecret": "", "stationId": "" },
    "google": { "clientId": "", "clientSecret": "", "refreshToken": "", "calendarId": "" },
    "dropbox": { "appKey": "", "refreshToken": "", "filePath": "" },
    "homeAssistant": { "url": "", "token": "" },
    "telegram": { "botToken": "", "allowedChatIds": [] },
    "messaging": { "apiToken": "" }
  },
  "tasks": { "source": "local", "tasksPath": "tasks.list", "remindersPath": "reminders.list" },
  "sections": [ { "id": "header", "enabled": true, "options": {} }, … ],
  "schedules": [ { "name": "Weekday morning", "enabled": true, "time": "06:30", "days": [1,2,3,4,5], "print": true } ],
  "print": { "command": "lp \"{pdf}\"" },
  "server": { "port": 8787, "printPort": 8788 }
}
```
