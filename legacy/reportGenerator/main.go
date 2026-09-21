package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"shared"

	"github.com/joho/godotenv"
)



type jokeResponse struct {
	Error    bool   `json:"error"`
	Type     string `json:"type"`
	Joke     string `json:"joke"`
	Setup    string `json:"setup"`
	Delivery string `json:"delivery"`
}

type AerisStats struct {
	CurrentTemp   float64
	Humidity      float64
	WindSpeed     float64
	RainToday     float64
	RainYesterday float64
	HasStats      bool
}

// PageData holds all the data needed to render the report template.
type PageData struct {
	Gdate           string
	TasksRems       []string
	CalendarEvents  []CalendarEvent
	HourlyForecasts []HourlyForecast
	DailyForecasts  []DailyForecast
	HNArticles      []hnArticle
	NYTArticles     []nytArticle
	Joke            string
	Aeris           *AerisStats
	HasFarside      bool
}



func getJoke() string {
	jokeURL := "https://v2.jokeapi.dev/joke/Miscellaneous,Pun?blacklistFlags=nsfw,racist,sexist,explicit"
	resp, err := http.Get(jokeURL)
	if err != nil {
		log.Printf("Warning: failed to fetch joke: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Warning: joke API returned status %d", resp.StatusCode)
		return ""
	}

	var jokeRes jokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&jokeRes); err != nil {
		log.Printf("Warning: failed to decode joke: %v", err)
		return ""
	}

	if jokeRes.Error {
		return ""
	}

	if jokeRes.Type == "single" {
		return jokeRes.Joke
	} else if jokeRes.Type == "twopart" {
		return fmt.Sprintf("%s\n\n%s", jokeRes.Setup, jokeRes.Delivery)
	}

	return ""
}

func getFarsideImage() bool {
	dir := "H:\\iCloudDrive\\Scripts\\25-printerProj\\jpgs\\farside"
	files, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("Warning: failed to read farside directory: %v", err)
		return false
	}

	var jpgs []string
	for _, entry := range files {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jpg") {
			jpgs = append(jpgs, entry.Name())
		}
	}

	if len(jpgs) == 0 {
		log.Printf("Warning: no farside jpg files found")
		return false
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	chosen := jpgs[r.Intn(len(jpgs))]

	srcPath := filepath.Join(dir, chosen)
	destPath := "farside.jpg"

	input, err := os.ReadFile(srcPath)
	if err != nil {
		log.Printf("Warning: failed to read chosen farside image: %v", err)
		return false
	}

	err = os.WriteFile(destPath, input, 0644)
	if err != nil {
		log.Printf("Warning: failed to write farside.jpg: %v", err)
		return false
	}

	return true
}

func getAerisStats() (*AerisStats, error) {
	stats := &AerisStats{HasStats: false}
	clientID := "wgE96YE3scTQLKjnqiMsv"
	clientSecret := "xNXIFQQtESG7YLIbSOb5eHLrt2DwtBCU8519KhSj"
	stationID := "pws_kalhoove43"

	// 1. Current Observations
	obsURL := fmt.Sprintf("https://api.aerisapi.com/observations/%s?client_id=%s&client_secret=%s", stationID, clientID, clientSecret)
	resp, err := http.Get(obsURL)
	if err != nil {
		return stats, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var obsRes struct {
			Success  bool            `json:"success"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&obsRes); err == nil && obsRes.Success {
			type ObStruct struct {
				Ob struct {
					TempF         *float64 `json:"tempF"`
					Humidity      *float64 `json:"humidity"`
					WindMPH       *float64 `json:"windMPH"`
					PrecipTodayIN *float64 `json:"precipTodayIN"`
				} `json:"ob"`
			}
			var ob ObStruct
			if bytes.HasPrefix(obsRes.Response, []byte("[")) {
				var list []ObStruct
				if err := json.Unmarshal(obsRes.Response, &list); err == nil && len(list) > 0 {
					ob = list[0]
				}
			} else {
				_ = json.Unmarshal(obsRes.Response, &ob)
			}

			if ob.Ob.TempF != nil {
				stats.CurrentTemp = *ob.Ob.TempF
			}
			if ob.Ob.Humidity != nil {
				stats.Humidity = *ob.Ob.Humidity
			}
			if ob.Ob.WindMPH != nil {
				stats.WindSpeed = *ob.Ob.WindMPH
			}
			if ob.Ob.PrecipTodayIN != nil {
				stats.RainToday = *ob.Ob.PrecipTodayIN
			}
			stats.HasStats = true
		}
	}

	// 2. Yesterday's Summary
	sumURL := fmt.Sprintf("https://api.aerisapi.com/observations/summary/%s?from=yesterday&to=yesterday&client_id=%s&client_secret=%s", stationID, clientID, clientSecret)
	respSum, err := http.Get(sumURL)
	if err != nil {
		return stats, nil
	}
	defer respSum.Body.Close()

	if respSum.StatusCode == http.StatusOK {
		var sumRes struct {
			Success  bool            `json:"success"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.NewDecoder(respSum.Body).Decode(&sumRes); err == nil && sumRes.Success {
			type PeriodStruct struct {
				Periods []struct {
					Summary struct {
						Precip struct {
							TotalIN *float64 `json:"totalIN"`
						} `json:"precip"`
					} `json:"summary"`
				} `json:"periods"`
			}
			var sum PeriodStruct
			if bytes.HasPrefix(sumRes.Response, []byte("[")) {
				var list []PeriodStruct
				if err := json.Unmarshal(sumRes.Response, &list); err == nil && len(list) > 0 {
					sum = list[0]
				}
			} else {
				_ = json.Unmarshal(sumRes.Response, &sum)
			}

			if len(sum.Periods) > 0 {
				prec := sum.Periods[0].Summary.Precip
				if prec.TotalIN != nil {
					stats.RainYesterday = *prec.TotalIN
				}
			}
		}
	}

	return stats, nil
}

func main() {
	godotenv.Load()

	// Run Google Calendar auth flow if requested
	if len(os.Args) > 1 && (os.Args[1] == "auth-google" || os.Args[1] == "-auth-google" || os.Args[1] == "--auth-google") {
		runGoogleAuth()
		return
	}

	// Run Dropbox CLI authentication flow if requested
	if len(os.Args) > 1 && (os.Args[1] == "auth" || os.Args[1] == "-auth" || os.Args[1] == "--auth") {
		runCLIAuth()
		return
	}

	tasksPath := os.Getenv("TASKS_LIST_PATH")
	if tasksPath == "" {
		tasksPath = "./tasks.list"
	}
	remindersPath := os.Getenv("REMINDERS_LIST_PATH")
	if remindersPath == "" {
		remindersPath = "./reminders.list"
	}

	// Create reminders file if it doesn't exist
	if err := shared.EnsureFile(remindersPath); err != nil {
		log.Fatalf("ensuring reminders file: %v", err)
	}

	// Create tasks file if we are using local fallback and it doesn't exist
	if !shared.IsDropboxEnabled() {
		if err := shared.EnsureFile(tasksPath); err != nil {
			log.Fatalf("ensuring tasks file: %v", err)
		}
	}

	// Get weather data
	hourlyForecasts, err := getHourlyForecast()
	if err != nil {
		log.Printf("Warning: getting hourly forecast failed: %v", err)
		hourlyForecasts = []HourlyForecast{}
	}
	dailyForecasts, err := getDailyForecast()
	if err != nil {
		log.Printf("Warning: getting daily forecast failed: %v", err)
		dailyForecasts = []DailyForecast{}
	}

	// Get next 4 Google Calendar events
	calendarEvents, err := getCalendarEvents(4)
	if err != nil {
		log.Printf("Warning: getting calendar events failed: %v", err)
		calendarEvents = []CalendarEvent{}
	}

	// Get tasks list
	var tasks []string
	if shared.IsDropboxEnabled() {
		db := shared.GetDropboxClient()
		tasks, err = db.GetTasks()
		if err != nil {
			log.Fatalf("reading tasks from dropbox: %v", err)
		}
	} else {
		tasks, err = shared.GetLines(tasksPath)
		if err != nil {
			log.Fatalf("reading tasks: %v", err)
		}
	}

	// Get reminders list and check which ones should be reminded today
	allReminders, err := shared.GetLines(remindersPath)
	if err != nil {
		log.Fatalf("reading reminders: %v", err)
	}

	var reminders []string
	now := time.Now()
	for _, r := range allReminders {
		rs := strings.Split(r, "|")
		if len(rs) == 2 && matchCronExpression(now, rs[0]) {
			reminders = append(reminders, rs[1])
		}
	}

	// Get HN articles (disabled for now)
	var articles []hnArticle
	/*
	articles, err := getHNArticles()
	if err != nil {
		log.Printf("Warning: getting HN articles failed: %v", err)
		articles = []hnArticle{}
	}
	if len(articles) > 3 {
		articles = articles[:3]
	}
	*/

	// Get NYT articles (take up to 5)
	nytArticles, err := getNYTArticles()
	if err != nil {
		log.Printf("Warning: getting NYT articles failed: %v", err)
		nytArticles = []nytArticle{}
	}
	if len(nytArticles) > 5 {
		nytArticles = nytArticles[:5]
	}

	// Current date/time
	gd := time.Now().Format("Mon Jan 2, 2006")

	// Get joke
	joke := getJoke()

	// Copy a random Far Side image
	hasFarside := getFarsideImage()

	// Get Aeris stats
	aerisStats, err := getAerisStats()
	if err != nil {
		log.Printf("Warning: getting Aeris stats failed: %v", err)
	}

	// Rendering the HTML template
	tmpl, err := template.ParseFiles("template.html")
	if err != nil {
		log.Fatalf("parsing template: %v", err)
	}

	f, err := os.Create("temp.html")
	if err != nil {
		log.Fatalf("creating temp file: %v", err)
	}

	pageData := PageData{
		Gdate:           gd,
		TasksRems:       append(tasks, reminders...),
		CalendarEvents:  calendarEvents,
		HourlyForecasts: hourlyForecasts,
		DailyForecasts:  dailyForecasts,
		HNArticles:      articles,
		NYTArticles:     nytArticles,
		Joke:            joke,
		Aeris:           aerisStats,
		HasFarside:      hasFarside,
	}

	err = tmpl.Execute(f, pageData)
	f.Close()
	if err != nil {
		log.Fatalf("executing template: %v", err)
	}

	pageHeight := estimatePageHeight(pageData)

	// Run a feedback loop to find the exact minimum page height that fits all content onto a single page.
	for attempt := 0; attempt < 25; attempt++ {
		var cmd *exec.Cmd
		pageHeightStr := fmt.Sprintf("%dmm", pageHeight)
		switch runtime.GOOS {
		case "windows":
			wkPath := ".\\wkhtmltopdf.exe"
			if _, err := os.Stat(wkPath); os.IsNotExist(err) {
				globalPath := "C:\\Program Files\\wkhtmltopdf\\bin\\wkhtmltopdf.exe"
				if _, err := os.Stat(globalPath); err == nil {
					wkPath = globalPath
				} else {
					wkPath = "wkhtmltopdf.exe"
				}
			}
			cmd = exec.Command(wkPath, "--encoding", "utf-8", "--margin-top", "1mm", "--margin-bottom", "7mm", "--margin-left", "0mm", "--margin-right",
				"0mm", "--page-height", pageHeightStr, "--page-width", "47mm", "--grayscale", "--disable-smart-shrinking", "--enable-local-file-access", "temp.html", "breaklist.pdf")
		default: //Mac & Linux
			shellCmd := fmt.Sprintf("wkhtmltopdf --encoding utf-8 --margin-top 1mm --margin-bottom 7mm --margin-left 0mm --margin-right 0mm --page-height %s --page-width 47mm --grayscale --disable-smart-shrinking --enable-local-file-access \"temp.html\" \"breaklist.pdf\"", pageHeightStr)
			cmd = exec.Command("sh", "-c", shellCmd)
		}

		_, err = cmd.Output()
		if err != nil {
			log.Fatalf("generating PDF (attempt %d): %v", attempt, err)
		}

		count, err := getPdfPageCount("breaklist.pdf")
		if err != nil || count <= 1 {
			// Done! We successfully fit all content onto a single page.
			log.Printf("Successfully generated 1-page PDF with height: %dmm", pageHeight)
			break
		}

		// Content overflowed to page 2, increase height and retry
		pageHeight += 10
	}

	log.Print("Created new report")
}

func estimatePageHeight(data PageData) int {
	// Let's calculate height in pixels first, then convert to mm.
	// Base padding and margins
	px := 10

	// Top date
	px += 16

	// Aeris Weather Stats
	if data.Aeris != nil && data.Aeris.HasStats {
		px += 55
	}
	px += 15 // Divider

	// Upcoming calendar
	if len(data.CalendarEvents) > 0 {
		px += 20 // Title
		for _, event := range data.CalendarEvents {
			// Title: full width (approx 34 chars per line)
			titleLines := (len(event.Title) + 33) / 34
			if titleLines < 1 {
				titleLines = 1
			}
			// Date: full width (approx 42 chars per line)
			dateLines := (len(event.DateTime) + 41) / 42
			if dateLines < 1 {
				dateLines = 1
			}
			px += titleLines*10 + dateLines*9 + 8
		}
		px += 15 // Divider
	}

	// Tasks list
	if len(data.TasksRems) > 0 {
		px += 10 // ul margins
		for _, task := range data.TasksRems {
			// Printable width is 32mm = 120px. At 11px font, fits ~32 chars per line.
			lines := (len(task) + 31) / 32
			if lines < 1 {
				lines = 1
			}
			px += lines*15 + 6
		}
		px += 15 // Divider
	}

	// Joke
	if data.Joke != "" {
		jokeText := strings.ReplaceAll(data.Joke, "\r", "")
		lines := 0
		for _, line := range strings.Split(jokeText, "\n") {
			lLines := (len(line) + 31) / 32 // 11px font fits ~32 chars per line
			if lLines < 1 {
				lLines = 1
			}
			lines += lLines
		}
		px += lines*14 + 16
		px += 15 // Divider
	}

	// Far Side Image
	if data.HasFarside {
		px += 187 // Image height (~177px) + margins
		px += 15  // Divider
	}

	// Hourly forecast (Today)
	if len(data.HourlyForecasts) > 0 {
		px += 20 // Title
		px += 72 // Table height + bottom margin
		px += 7  // Thin divider
	}

	// Daily forecast (4-Day Outlook)
	if len(data.DailyForecasts) > 0 {
		px += 20 // Title
		px += len(data.DailyForecasts) * 26
		px += 15 // Divider
	}

	// NYT Articles
	if len(data.NYTArticles) > 0 {
		px += 20 // Title
		px += 9  // hr
		px += 10 // ul margins
		for _, art := range data.NYTArticles {
			// Title fits ~36 chars per line at 10px font
			lines := (len(art.Title) + 35) / 36
			if lines < 1 {
				lines = 1
			}
			px += lines*14 + 6
		}
		px += 15 // Divider
	}

	// HN Articles
	if len(data.HNArticles) > 0 {
		for _, art := range data.HNArticles {
			lines := (len(art.Title) + 35) / 36
			summaryLines := (len(art.Summary) + 35) / 36
			px += (lines + summaryLines)*14 + 50
		}
	}

	// Convert pixels to mm (96 DPI)
	mmHeight := int(float64(px) * 25.4 / 96.0)

	// Add a safety buffer (in mm)
	mmHeight += 8

	return mmHeight
}

func getPdfPageCount(filename string) (int, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return 0, err
	}
	idx := bytes.Index(data, []byte("/Count "))
	if idx == -1 {
		return 1, nil
	}
	start := idx + len("/Count ")
	end := start
	for end < len(data) && data[end] >= '0' && data[end] <= '9' {
		end++
	}
	if end == start {
		return 1, nil
	}
	count, err := strconv.Atoi(string(data[start:end]))
	if err != nil {
		return 1, nil
	}
	return count, nil
}
