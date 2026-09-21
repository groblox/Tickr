// Package connectors holds clients for the external services Breaklist talks to.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"time"

	"breaklist/internal/config"
	"breaklist/internal/httpx"
)

// Hour is one hourly forecast slot. Temperatures are Celsius.
type Hour struct {
	Time       time.Time
	TempC      float64
	FeelsC     float64
	DewC       float64
	PrecipProb int
	Code       int // Tomorrow.io weather code (used for icons)
	IsDay      bool
}

// Day is one daily forecast row. Temperatures are Celsius.
type Day struct {
	Date       time.Time
	MaxC       float64
	MinC       float64
	DewC       float64
	PrecipProb int
	Code       int
	Sunrise    time.Time
	Sunset     time.Time
	WindMaxKmh float64
	PrecipMM   float64
	UVIndex    float64
}

// Forecast is the provider-neutral weather result.
type Forecast struct {
	Provider string
	Hourly   []Hour
	Daily    []Day
}

// GetForecast picks the configured provider.
func GetForecast(ctx context.Context, cfg *config.Config) (*Forecast, error) {
	lat, lng, ok := cfg.LatLng()
	if !ok {
		return nil, fmt.Errorf("location is not set (expected \"lat,lng\" in General settings)")
	}
	loc := cfg.Location()
	switch cfg.Connectors.Weather.Provider {
	case "tomorrowio":
		if cfg.Connectors.Weather.TomorrowAPIKey == "" {
			return nil, fmt.Errorf("Tomorrow.io API key is not set")
		}
		return tomorrowForecast(ctx, cfg.Connectors.Weather.TomorrowAPIKey, cfg.General.Location, loc)
	default:
		return openMeteoForecast(ctx, lat, lng, cfg.General.Timezone, loc)
	}
}

// ── Open-Meteo (no key) ──────────────────────────────────────────────────────

type openMeteoResp struct {
	Hourly struct {
		Time        []string  `json:"time"`
		Temp        []float64 `json:"temperature_2m"`
		Apparent    []float64 `json:"apparent_temperature"`
		Dew         []float64 `json:"dew_point_2m"`
		PrecipProb  []*int    `json:"precipitation_probability"`
		WeatherCode []int     `json:"weather_code"`
		IsDay       []int     `json:"is_day"`
	} `json:"hourly"`
	Daily struct {
		Time        []string  `json:"time"`
		Max         []float64 `json:"temperature_2m_max"`
		Min         []float64 `json:"temperature_2m_min"`
		PrecipProb  []*int    `json:"precipitation_probability_max"`
		WeatherCode []int     `json:"weather_code"`
		Sunrise     []string  `json:"sunrise"`
		Sunset      []string  `json:"sunset"`
		WindMax     []float64 `json:"wind_speed_10m_max"`
		PrecipSum   []float64 `json:"precipitation_sum"`
		UVMax       []float64 `json:"uv_index_max"`
	} `json:"daily"`
}

func openMeteoForecast(ctx context.Context, lat, lng float64, tz string, loc *time.Location) (*Forecast, error) {
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lng))
	q.Set("hourly", "temperature_2m,apparent_temperature,dew_point_2m,precipitation_probability,weather_code,is_day")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_probability_max,weather_code,sunrise,sunset,wind_speed_10m_max,precipitation_sum,uv_index_max")
	q.Set("timezone", tz)
	q.Set("forecast_days", "6")
	var resp openMeteoResp
	if err := httpx.GetJSON(ctx, "https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil, &resp); err != nil {
		return nil, fmt.Errorf("open-meteo: %w", err)
	}
	fc := &Forecast{Provider: "Open-Meteo"}
	for i, ts := range resp.Hourly.Time {
		t, err := time.ParseInLocation("2006-01-02T15:04", ts, loc)
		if err != nil {
			continue
		}
		h := Hour{Time: t, Code: wmoToTomorrow(at(resp.Hourly.WeatherCode, i))}
		h.TempC = at(resp.Hourly.Temp, i)
		h.FeelsC = at(resp.Hourly.Apparent, i)
		h.DewC = at(resp.Hourly.Dew, i)
		h.IsDay = at(resp.Hourly.IsDay, i) == 1
		if i < len(resp.Hourly.PrecipProb) && resp.Hourly.PrecipProb[i] != nil {
			h.PrecipProb = *resp.Hourly.PrecipProb[i]
		}
		fc.Hourly = append(fc.Hourly, h)
	}
	for i, ds := range resp.Daily.Time {
		t, err := time.ParseInLocation("2006-01-02", ds, loc)
		if err != nil {
			continue
		}
		d := Day{Date: t, Code: wmoToTomorrow(at(resp.Daily.WeatherCode, i))}
		d.MaxC = at(resp.Daily.Max, i)
		d.MinC = at(resp.Daily.Min, i)
		d.WindMaxKmh = at(resp.Daily.WindMax, i)
		d.PrecipMM = at(resp.Daily.PrecipSum, i)
		d.UVIndex = at(resp.Daily.UVMax, i)
		if i < len(resp.Daily.PrecipProb) && resp.Daily.PrecipProb[i] != nil {
			d.PrecipProb = *resp.Daily.PrecipProb[i]
		}
		if i < len(resp.Daily.Sunrise) {
			d.Sunrise, _ = time.ParseInLocation("2006-01-02T15:04", resp.Daily.Sunrise[i], loc)
		}
		if i < len(resp.Daily.Sunset) {
			d.Sunset, _ = time.ParseInLocation("2006-01-02T15:04", resp.Daily.Sunset[i], loc)
		}
		// Daily dew point: average of that day's hourly values.
		var sum float64
		var n int
		for _, h := range fc.Hourly {
			if h.Time.YearDay() == t.YearDay() && h.Time.Year() == t.Year() {
				sum += h.DewC
				n++
			}
		}
		if n > 0 {
			d.DewC = sum / float64(n)
		}
		fc.Daily = append(fc.Daily, d)
	}
	if len(fc.Hourly) == 0 && len(fc.Daily) == 0 {
		return nil, fmt.Errorf("open-meteo returned no data")
	}
	return fc, nil
}

func at[T any](s []T, i int) T {
	var zero T
	if i < len(s) {
		return s[i]
	}
	return zero
}

// wmoToTomorrow maps WMO weather interpretation codes onto Tomorrow.io codes so
// one icon set serves both providers.
func wmoToTomorrow(code int) int {
	switch code {
	case 0:
		return 1000
	case 1:
		return 1100
	case 2:
		return 1101
	case 3:
		return 1001
	case 45, 48:
		return 2000
	case 51, 53, 55:
		return 4000
	case 56, 57:
		return 6000
	case 61, 80:
		return 4200
	case 63, 81:
		return 4001
	case 65, 82:
		return 4201
	case 66:
		return 6200
	case 67:
		return 6201
	case 71, 85:
		return 5100
	case 73:
		return 5000
	case 75, 86:
		return 5101
	case 77:
		return 5001
	case 95, 96, 99:
		return 8000
	}
	return 1000
}

// ── Tomorrow.io ──────────────────────────────────────────────────────────────

type tomorrowResp struct {
	Data struct {
		Timelines []struct {
			Timestep  string `json:"timestep"`
			Intervals []struct {
				StartTime time.Time                  `json:"startTime"`
				Values    map[string]json.RawMessage `json:"values"`
			} `json:"intervals"`
		} `json:"timelines"`
	} `json:"data"`
}

func tomorrowForecast(ctx context.Context, key, location string, loc *time.Location) (*Forecast, error) {
	endpoint := "https://api.tomorrow.io/v4/timelines?apikey=" + url.QueryEscape(key)
	payload := map[string]any{
		"location": location,
		"fields": []string{"temperature", "temperatureApparent", "dewPoint", "precipitationProbability", "weatherCode",
			"temperatureMax", "temperatureMin", "sunriseTime", "sunsetTime", "windSpeedMax", "precipitationIntensity", "uvIndex"},
		"units":     "metric",
		"timesteps": []string{"1h", "1d"},
		"startTime": "now",
		"endTime":   "nowPlus5d",
		"timezone":  loc.String(),
	}
	var resp tomorrowResp
	if err := httpx.PostJSON(ctx, endpoint, payload, nil, &resp); err != nil {
		return nil, fmt.Errorf("tomorrow.io: %w", err)
	}
	fc := &Forecast{Provider: "Tomorrow.io"}
	num := func(v map[string]json.RawMessage, key string) float64 {
		var f float64
		if raw, ok := v[key]; ok {
			_ = json.Unmarshal(raw, &f)
		}
		return f
	}
	when := func(v map[string]json.RawMessage, key string) time.Time {
		var s string
		if raw, ok := v[key]; ok {
			_ = json.Unmarshal(raw, &s)
		}
		t, _ := time.Parse(time.RFC3339, s)
		return t.In(loc)
	}
	for _, tl := range resp.Data.Timelines {
		for _, iv := range tl.Intervals {
			t := iv.StartTime.In(loc)
			v := iv.Values
			switch tl.Timestep {
			case "1h":
				fc.Hourly = append(fc.Hourly, Hour{
					Time: t, TempC: num(v, "temperature"), FeelsC: num(v, "temperatureApparent"), DewC: num(v, "dewPoint"),
					PrecipProb: int(math.Round(num(v, "precipitationProbability"))), Code: int(num(v, "weatherCode")),
					IsDay: t.Hour() >= 6 && t.Hour() < 20,
				})
			case "1d":
				fc.Daily = append(fc.Daily, Day{
					Date: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc),
					MaxC: num(v, "temperatureMax"), MinC: num(v, "temperatureMin"), DewC: num(v, "dewPoint"),
					PrecipProb: int(math.Round(num(v, "precipitationProbability"))), Code: int(num(v, "weatherCode")),
					WindMaxKmh: num(v, "windSpeedMax") * 3.6, PrecipMM: num(v, "precipitationIntensity") * 24, UVIndex: num(v, "uvIndex"),
					Sunrise: when(v, "sunriseTime"), Sunset: when(v, "sunsetTime"),
				})
			}
		}
	}
	if len(fc.Hourly) == 0 && len(fc.Daily) == 0 {
		return nil, fmt.Errorf("tomorrow.io returned no timelines")
	}
	return fc, nil
}

// IconCode returns the icon file stem for a weather code and day flag.
func IconCode(code int, isDay bool) string {
	suffix := "0"
	if !isDay {
		suffix = "1"
	}
	return fmt.Sprintf("%d%s", code, suffix)
}

// Describe turns a Tomorrow.io code into a short phrase.
func Describe(code int) string {
	switch code {
	case 1000:
		return "clear"
	case 1100:
		return "mostly clear"
	case 1101:
		return "partly cloudy"
	case 1102:
		return "mostly cloudy"
	case 1001:
		return "cloudy"
	case 2000, 2100:
		return "foggy"
	case 4000:
		return "drizzle"
	case 4001:
		return "rain"
	case 4200:
		return "light rain"
	case 4201:
		return "heavy rain"
	case 5000:
		return "snow"
	case 5001:
		return "flurries"
	case 5100:
		return "light snow"
	case 5101:
		return "heavy snow"
	case 6000, 6001, 6200, 6201:
		return "freezing rain"
	case 7000, 7101, 7102:
		return "ice pellets"
	case 8000:
		return "thunderstorm"
	}
	return "unsettled"
}
