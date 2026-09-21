package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// ── Hourly API response ───────────────────────────────────────────────────────

type hourlyWeatherResponse struct {
	Data struct {
		Timelines []struct {
			Timestep  string    `json:"timestep"`
			EndTime   time.Time `json:"endTime"`
			StartTime time.Time `json:"startTime"`
			Intervals []struct {
				StartTime time.Time `json:"startTime"`
				Values    struct {
					Temperature             float64 `json:"temperature"`
					TemperatureApparent     float64 `json:"temperatureApparent"`
					DewPoint                float64 `json:"dewPoint"`
					PrecipitationProbability float64 `json:"precipitationProbability"`
					WeatherCode             int     `json:"weatherCode"`
				} `json:"values"`
			} `json:"intervals"`
		} `json:"timelines"`
	} `json:"data"`
}

// ── Daily API response ────────────────────────────────────────────────────────

type dailyWeatherResponse struct {
	Data struct {
		Timelines []struct {
			Timestep  string    `json:"timestep"`
			EndTime   time.Time `json:"endTime"`
			StartTime time.Time `json:"startTime"`
			Intervals []struct {
				StartTime time.Time `json:"startTime"`
				Values    struct {
					TemperatureMax           float64 `json:"temperatureMax"`
					TemperatureMin           float64 `json:"temperatureMin"`
					DewPoint                 float64 `json:"dewPoint"`
					PrecipitationProbability  float64 `json:"precipitationProbability"`
					WeatherCode              int     `json:"weatherCode"`
				} `json:"values"`
			} `json:"intervals"`
		} `json:"timelines"`
	} `json:"data"`
}

// ── Public forecast types used by template ────────────────────────────────────

// HourlyForecast holds one 3-hour slot for the today strip.
type HourlyForecast struct {
	TimeString  string // "09:00"
	Temp        string // "81°F"
	FeelsLike   string // "83°F"
	DewPoint    string // "65°F"
	PrecipProb  int    // 40
	WeatherCode int
}

// DailyForecast holds one day row for the 4-day table.
type DailyForecast struct {
	DayName     string // "Mon"
	DateStr     string // "Jun 4"
	TempHigh    string // "88°F"
	TempLow     string // "67°F"
	DewPoint    string // "62°F"
	PrecipProb  int    // 30
	WeatherCode int
}

// ── Unit helpers ──────────────────────────────────────────────────────────────

func cToF(c float64) float64 {
	return math.Round(c*9/5 + 32)
}

func fmtF(c float64) string {
	return fmt.Sprintf("%.0f°F", cToF(c))
}

// ── Hourly forecast (3-hour intervals, rest of today) ─────────────────────────

// getHourlyForecast fetches 1h timestep data from Tomorrow.io and returns
// every 3rd interval that falls within the current calendar day, in local time.
func getHourlyForecast() ([]HourlyForecast, error) {
	apiKey := os.Getenv("TOMORROW_API_KEY")
	location := os.Getenv("LOCATION")
	url := fmt.Sprintf("https://api.tomorrow.io/v4/timelines?apikey=%s", apiKey)

	body := fmt.Sprintf(
		`{"location":"%s","fields":["temperature","temperatureApparent","dewPoint","precipitationProbability","weatherCode"],"units":"metric","timesteps":["1h"],"startTime":"now","endTime":"nowPlus24h"}`,
		location,
	)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating hourly request: %w", err)
	}
	req.Header.Add("content-type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching hourly weather: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading hourly response: %w", err)
	}

	var data hourlyWeatherResponse
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parsing hourly JSON: %w", err)
	}
	if len(data.Data.Timelines) == 0 {
		return nil, fmt.Errorf("no hourly timelines returned")
	}

	loc, err := time.LoadLocation(os.Getenv("TIMEZONE"))
	if err != nil {
		return nil, fmt.Errorf("loading timezone: %w", err)
	}

	now := time.Now().In(loc)
	todayEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)

	var forecasts []HourlyForecast
	for i, iv := range data.Data.Timelines[0].Intervals {
		t := iv.StartTime.In(loc)
		// Only today's hours, every 3rd slot
		if t.After(todayEnd) {
			break
		}
		if i%3 != 0 {
			continue
		}
		forecasts = append(forecasts, HourlyForecast{
			TimeString:  t.Format("3 PM"),
			Temp:        fmtF(iv.Values.Temperature),
			FeelsLike:   fmtF(iv.Values.TemperatureApparent),
			DewPoint:    fmtF(iv.Values.DewPoint),
			PrecipProb:  int(math.Round(iv.Values.PrecipitationProbability)),
			WeatherCode: iv.Values.WeatherCode,
		})
		if len(forecasts) == 3 {
			break
		}
	}

	return forecasts, nil
}

// ── Daily forecast (next 4 days) ──────────────────────────────────────────────

// getDailyForecast fetches 1d timestep data from Tomorrow.io and returns
// the next 4 calendar days (tomorrow through +4).
func getDailyForecast() ([]DailyForecast, error) {
	apiKey := os.Getenv("TOMORROW_API_KEY")
	location := os.Getenv("LOCATION")
	url := fmt.Sprintf("https://api.tomorrow.io/v4/timelines?apikey=%s", apiKey)

	body := fmt.Sprintf(
		`{"location":"%s","fields":["temperatureMax","temperatureMin","dewPoint","precipitationProbability","weatherCode"],"units":"metric","timesteps":["1d"],"startTime":"now","endTime":"nowPlus5d"}`,
		location,
	)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating daily request: %w", err)
	}
	req.Header.Add("content-type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching daily weather: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading daily response: %w", err)
	}

	var data dailyWeatherResponse
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parsing daily JSON: %w", err)
	}
	if len(data.Data.Timelines) == 0 {
		return nil, fmt.Errorf("no daily timelines returned")
	}

	loc, err := time.LoadLocation(os.Getenv("TIMEZONE"))
	if err != nil {
		return nil, fmt.Errorf("loading timezone: %w", err)
	}

	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	var forecasts []DailyForecast
	for _, iv := range data.Data.Timelines[0].Intervals {
		t := iv.StartTime.In(loc)
		dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		// Skip today itself, only take future days
		if !dayStart.After(today) {
			continue
		}
		forecasts = append(forecasts, DailyForecast{
			DayName:     t.Format("Mon"),
			DateStr:     t.Format("Jan 2"),
			TempHigh:    fmtF(iv.Values.TemperatureMax),
			TempLow:     fmtF(iv.Values.TemperatureMin),
			DewPoint:    fmtF(iv.Values.DewPoint),
			PrecipProb:  int(math.Round(iv.Values.PrecipitationProbability)),
			WeatherCode: iv.Values.WeatherCode,
		})
		if len(forecasts) == 4 {
			break
		}
	}

	return forecasts, nil
}
