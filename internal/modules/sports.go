package modules

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tickr/internal/httpx"
)

// ── College football (ESPN public API) ───────────────────────────────────────

type cfbModule struct{}

const espnCFB = "https://site.api.espn.com/apis/site/v2/sports/football/college-football"

type rankRow struct {
	Rank   int
	Team   string
	Record string
	Trend  string
}

type gameRow struct {
	Team   string // "Alabama"
	Line   string // "Sat Sep 19, 2:30 PM · vs #12 Florida State · ABC"
	Result string // "Last: W 45-17 at Kentucky"
}

var cfbTpl = Tpl("cfb", `<div class="h">{{.Title}}</div>
{{if .Ranks}}<table class="ranks">{{range .Ranks}}<tr><td class="rk">{{.Rank}}</td><td>{{.Team}}</td><td class="rec">{{.Record}}</td><td class="tr">{{.Trend}}</td></tr>{{end}}</table>{{end}}
{{if .Games}}{{if .Ranks}}<div class="thin"></div>{{end}}{{range .Games}}<div class="game"><b>{{.Team}}</b><div class="s">{{.Line}}</div>{{if .Result}}<div class="s">{{.Result}}</div>{{end}}</div>{{end}}{{end}}
{{if .Matchups}}<div class="thin"></div><div class="s" style="font-weight:bold;margin-top:2px">Ranked matchups this week</div>{{range .Matchups}}<div class="s">{{.}}</div>{{end}}{{end}}`)

func (cfbModule) Info() Info {
	return Info{
		ID: "cfb", Name: "College football", Category: CatNews, DefaultEnabled: false, Source: "espn.com",
		Description: "AP Top 25 (or Coaches Poll), your teams' next game and last result, and this week's ranked-vs-ranked matchups. In season: late August through the playoff.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "College football"},
			{Key: "poll", Label: "Poll", Type: FieldSelect, Options: []string{"AP Top 25", "AFCA Coaches Poll"}, Default: "AP Top 25"},
			{Key: "topN", Label: "Teams to list (0 = none)", Type: FieldNumber, Default: 10, Min: F64(0), Max: F64(25)},
			{Key: "teams", Label: "Your teams", Type: FieldList, Default: "Alabama\nAuburn", Help: "One per line: school name or abbreviation (Alabama, AUB, Georgia…)"},
			{Key: "showLast", Label: "Show last result", Type: FieldBool, Default: true},
			{Key: "matchups", Label: "Show ranked matchups this week", Type: FieldBool, Default: true},
		},
	}
}

func (cfbModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	data := struct {
		Title    string
		Ranks    []rankRow
		Games    []gameRow
		Matchups []string
	}{Title: opt.Str("title", "College football")}

	// Rankings.
	if n := opt.Int("topN", 10); n > 0 {
		var res struct {
			Rankings []struct {
				Name  string `json:"name"`
				Ranks []struct {
					Current       int    `json:"current"`
					RecordSummary string `json:"recordSummary"`
					Trend         string `json:"trend"`
					Team          struct {
						Location     string `json:"location"`
						Abbreviation string `json:"abbreviation"`
					} `json:"team"`
				} `json:"ranks"`
			} `json:"rankings"`
		}
		if err := httpx.GetJSON(ctx, espnCFB+"/rankings", nil, &res); err != nil {
			return nil, fmt.Errorf("espn rankings: %w", err)
		}
		want := opt.Str("poll", "AP Top 25")
		for _, r := range res.Rankings {
			if !strings.EqualFold(r.Name, want) {
				continue
			}
			for _, t := range r.Ranks {
				if t.Current > n {
					continue
				}
				trend := t.Trend
				if trend == "-" || trend == "" {
					trend = ""
				}
				data.Ranks = append(data.Ranks, rankRow{Rank: t.Current, Team: t.Team.Location, Record: t.RecordSummary, Trend: trend})
			}
			break
		}
	}

	// Your teams: next game and last result.
	teams := opt.List("teams")
	if len(teams) > 0 {
		ids, err := lookupTeams(ctx, teams)
		if err != nil {
			env.Log("college football: %v", err)
		}
		for _, t := range ids {
			row, err := teamGames(ctx, env, t.id, t.name, opt.Bool("showLast", true))
			if err != nil {
				env.Log("college football %s: %v", t.name, err)
				continue
			}
			if row != nil {
				data.Games = append(data.Games, *row)
			}
		}
	}

	// Ranked matchups this week.
	if opt.Bool("matchups", true) {
		var sb struct {
			Events []struct {
				Date         string `json:"date"`
				Competitions []struct {
					Competitors []struct {
						HomeAway string `json:"homeAway"`
						Team     struct {
							Location string `json:"location"`
						} `json:"team"`
						CuratedRank struct {
							Current int `json:"current"`
						} `json:"curatedRank"`
					} `json:"competitors"`
					Broadcasts []struct {
						Names []string `json:"names"`
					} `json:"broadcasts"`
				} `json:"competitions"`
				Status struct {
					Type struct {
						Completed bool `json:"completed"`
					} `json:"type"`
				} `json:"status"`
			} `json:"events"`
		}
		if err := httpx.GetJSON(ctx, espnCFB+"/scoreboard?groups=80&limit=200", nil, &sb); err == nil {
			for _, e := range sb.Events {
				if e.Status.Type.Completed || len(e.Competitions) == 0 {
					continue
				}
				c := e.Competitions[0]
				if len(c.Competitors) != 2 {
					continue
				}
				a, b := c.Competitors[0], c.Competitors[1]
				if a.CuratedRank.Current > 25 || b.CuratedRank.Current > 25 || a.CuratedRank.Current == 0 || b.CuratedRank.Current == 0 {
					continue
				}
				home, away := a, b
				if a.HomeAway != "home" {
					home, away = b, a
				}
				when := ""
				if t, ok := parseESPNTime(e.Date); ok {
					when = t.In(env.Loc).Format("Mon 3:04 PM")
				}
				line := fmt.Sprintf("#%d %s at #%d %s · %s", away.CuratedRank.Current, away.Team.Location, home.CuratedRank.Current, home.Team.Location, when)
				if len(c.Broadcasts) > 0 && len(c.Broadcasts[0].Names) > 0 {
					line += " · " + c.Broadcasts[0].Names[0]
				}
				data.Matchups = append(data.Matchups, line)
			}
		}
	}

	if len(data.Ranks) == 0 && len(data.Games) == 0 && len(data.Matchups) == 0 {
		return EmptySection("cfb"), nil
	}
	est := 20 + len(data.Ranks)*11 + len(data.Games)*30 + len(data.Matchups)*10
	return Exec("cfb", cfbTpl, data, est)
}

type teamRef struct {
	id, name string
}

// parseESPNTime accepts ESPN's "2026-09-19T19:30Z" (no seconds) as well as RFC 3339.
func parseESPNTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// lookupTeams resolves names/abbreviations to ESPN team ids.
func lookupTeams(ctx context.Context, wanted []string) ([]teamRef, error) {
	var res struct {
		Sports []struct {
			Leagues []struct {
				Teams []struct {
					Team struct {
						ID           string `json:"id"`
						Location     string `json:"location"`
						Name         string `json:"name"`
						DisplayName  string `json:"displayName"`
						Abbreviation string `json:"abbreviation"`
					} `json:"team"`
				} `json:"teams"`
			} `json:"leagues"`
		} `json:"sports"`
	}
	if err := httpx.GetJSON(ctx, espnCFB+"/teams?limit=1000", nil, &res); err != nil {
		return nil, err
	}
	type team struct{ id, location, name, display, abbr string }
	var all []team
	for _, s := range res.Sports {
		for _, l := range s.Leagues {
			for _, t := range l.Teams {
				all = append(all, team{t.Team.ID, t.Team.Location, t.Team.Name, t.Team.DisplayName, t.Team.Abbreviation})
			}
		}
	}
	var out []teamRef
	for _, w := range wanted {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		lw := strings.ToLower(w)
		// Exact matches win over prefix matches so "Alabama" is not "Alabama A&M".
		var hit *team
		for i := range all {
			t := &all[i]
			if strings.EqualFold(t.abbr, w) || strings.EqualFold(t.location, w) || strings.EqualFold(t.display, w) {
				hit = t
				break
			}
		}
		if hit == nil {
			for i := range all {
				t := &all[i]
				if strings.HasPrefix(strings.ToLower(t.display), lw) || strings.HasPrefix(strings.ToLower(t.location), lw) {
					hit = t
					break
				}
			}
		}
		if hit != nil {
			out = append(out, teamRef{hit.id, hit.location})
		}
	}
	return out, nil
}

// teamGames fetches a team's schedule and summarises the next game and last result.
func teamGames(ctx context.Context, env *Env, id, name string, showLast bool) (*gameRow, error) {
	var res struct {
		Events []struct {
			Date         string `json:"date"`
			Competitions []struct {
				Competitors []struct {
					HomeAway string `json:"homeAway"`
					Winner   bool   `json:"winner"`
					Score    struct {
						DisplayValue string `json:"displayValue"`
					} `json:"score"`
					Team struct {
						ID       string `json:"id"`
						Location string `json:"location"`
					} `json:"team"`
					CuratedRank struct {
						Current int `json:"current"`
					} `json:"curatedRank"`
				} `json:"competitors"`
				Broadcasts []struct {
					Media struct {
						ShortName string `json:"shortName"`
					} `json:"media"`
				} `json:"broadcasts"`
				Status struct {
					Type struct {
						Completed   bool   `json:"completed"`
						ShortDetail string `json:"shortDetail"`
					} `json:"type"`
				} `json:"status"`
			} `json:"competitions"`
		} `json:"events"`
	}
	if err := httpx.GetJSON(ctx, fmt.Sprintf("%s/teams/%s/schedule", espnCFB, id), nil, &res); err != nil {
		return nil, err
	}
	row := &gameRow{Team: name}
	for _, e := range res.Events {
		if len(e.Competitions) == 0 || len(e.Competitions[0].Competitors) != 2 {
			continue
		}
		c := e.Competitions[0]
		var me, opp *struct {
			HomeAway string `json:"homeAway"`
			Winner   bool   `json:"winner"`
			Score    struct {
				DisplayValue string `json:"displayValue"`
			} `json:"score"`
			Team struct {
				ID       string `json:"id"`
				Location string `json:"location"`
			} `json:"team"`
			CuratedRank struct {
				Current int `json:"current"`
			} `json:"curatedRank"`
		}
		if c.Competitors[0].Team.ID == id {
			me, opp = &c.Competitors[0], &c.Competitors[1]
		} else {
			me, opp = &c.Competitors[1], &c.Competitors[0]
		}
		oppName := opp.Team.Location
		if opp.CuratedRank.Current > 0 && opp.CuratedRank.Current <= 25 {
			oppName = fmt.Sprintf("#%d %s", opp.CuratedRank.Current, oppName)
		}
		prep := "vs"
		if me.HomeAway == "away" {
			prep = "at"
		}
		if c.Status.Type.Completed {
			if showLast {
				wl := "L"
				if me.Winner {
					wl = "W"
				}
				row.Result = fmt.Sprintf("Last: %s %s-%s %s %s", wl, me.Score.DisplayValue, opp.Score.DisplayValue, prep, oppName)
			}
			continue
		}
		if row.Line == "" {
			when := "TBD"
			if t, ok := parseESPNTime(e.Date); ok {
				when = t.In(env.Loc).Format("Mon Jan 2, 3:04 PM")
				if strings.Contains(c.Status.Type.ShortDetail, "TBD") {
					when = t.In(env.Loc).Format("Mon Jan 2") + ", TBD"
				}
			}
			row.Line = fmt.Sprintf("%s · %s %s", when, prep, oppName)
			if len(c.Broadcasts) > 0 && c.Broadcasts[0].Media.ShortName != "" {
				row.Line += " · " + c.Broadcasts[0].Media.ShortName
			}
		}
	}
	if row.Line == "" && row.Result == "" {
		return nil, nil
	}
	if row.Line == "" {
		row.Line = "Season complete"
	}
	return row, nil
}

func init() {
	Register(cfbModule{})
}
