package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"tickr/internal/config"
	"tickr/internal/httpx"
)

// PWSStats are the current readings of a personal weather station.
type PWSStats struct {
	TempC         float64
	FeelsLikeC    float64
	Humidity      float64
	DewpointC     float64
	WindKmh       float64
	WindGustKmh   float64
	PressureMB    float64
	RainTodayMM   float64
	RainYesterday float64 // mm
	HasStats      bool
}

// GetPWSStats fetches current observations and yesterday's rain from Aeris/Xweather.
func GetPWSStats(ctx context.Context, a config.Aeris) (*PWSStats, error) {
	if a.ClientID == "" || a.ClientSecret == "" || a.StationID == "" {
		return nil, fmt.Errorf("Aeris client id, secret and station id are required")
	}
	stats := &PWSStats{}
	auth := fmt.Sprintf("client_id=%s&client_secret=%s", url.QueryEscape(a.ClientID), url.QueryEscape(a.ClientSecret))

	var obs struct {
		Success  bool                          `json:"success"`
		Error    *struct{ Description string } `json:"error"`
		Response json.RawMessage               `json:"response"`
	}
	obsURL := fmt.Sprintf("https://api.aerisapi.com/observations/%s?%s", url.PathEscape(a.StationID), auth)
	if err := httpx.GetJSON(ctx, obsURL, nil, &obs); err != nil {
		return nil, fmt.Errorf("aeris observations: %w", err)
	}
	if !obs.Success {
		msg := "unknown error"
		if obs.Error != nil {
			msg = obs.Error.Description
		}
		return nil, fmt.Errorf("aeris observations: %s", msg)
	}
	type obStruct struct {
		Ob struct {
			TempC         *float64 `json:"tempC"`
			FeelslikeC    *float64 `json:"feelslikeC"`
			Humidity      *float64 `json:"humidity"`
			DewpointC     *float64 `json:"dewpointC"`
			WindKPH       *float64 `json:"windKPH"`
			WindGustKPH   *float64 `json:"windGustKPH"`
			PressureMB    *float64 `json:"pressureMB"`
			PrecipTodayMM *float64 `json:"precipTodayMM"`
		} `json:"ob"`
	}
	var ob obStruct
	if bytes.HasPrefix(bytes.TrimSpace(obs.Response), []byte("[")) {
		var list []obStruct
		if err := json.Unmarshal(obs.Response, &list); err == nil && len(list) > 0 {
			ob = list[0]
		}
	} else {
		_ = json.Unmarshal(obs.Response, &ob)
	}
	if ob.Ob.TempC != nil {
		stats.TempC = *ob.Ob.TempC
	}
	if ob.Ob.FeelslikeC != nil {
		stats.FeelsLikeC = *ob.Ob.FeelslikeC
	}
	if ob.Ob.Humidity != nil {
		stats.Humidity = *ob.Ob.Humidity
	}
	if ob.Ob.DewpointC != nil {
		stats.DewpointC = *ob.Ob.DewpointC
	}
	if ob.Ob.WindKPH != nil {
		stats.WindKmh = *ob.Ob.WindKPH
	}
	if ob.Ob.WindGustKPH != nil {
		stats.WindGustKmh = *ob.Ob.WindGustKPH
	}
	if ob.Ob.PressureMB != nil {
		stats.PressureMB = *ob.Ob.PressureMB
	}
	if ob.Ob.PrecipTodayMM != nil {
		stats.RainTodayMM = *ob.Ob.PrecipTodayMM
	}
	stats.HasStats = true

	// Yesterday's rain total is best-effort.
	var sum struct {
		Success  bool            `json:"success"`
		Response json.RawMessage `json:"response"`
	}
	sumURL := fmt.Sprintf("https://api.aerisapi.com/observations/summary/%s?from=yesterday&to=yesterday&%s", url.PathEscape(a.StationID), auth)
	if err := httpx.GetJSON(ctx, sumURL, nil, &sum); err == nil && sum.Success {
		type periodStruct struct {
			Periods []struct {
				Summary struct {
					Precip struct {
						TotalMM *float64 `json:"totalMM"`
					} `json:"precip"`
				} `json:"summary"`
			} `json:"periods"`
		}
		var ps periodStruct
		if bytes.HasPrefix(bytes.TrimSpace(sum.Response), []byte("[")) {
			var list []periodStruct
			if err := json.Unmarshal(sum.Response, &list); err == nil && len(list) > 0 {
				ps = list[0]
			}
		} else {
			_ = json.Unmarshal(sum.Response, &ps)
		}
		if len(ps.Periods) > 0 && ps.Periods[0].Summary.Precip.TotalMM != nil {
			stats.RainYesterday = *ps.Periods[0].Summary.Precip.TotalMM
		}
	}
	return stats, nil
}
