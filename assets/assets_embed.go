// Package assets embeds static files (weather icons) into the binary.
package assets

import (
	"embed"
	"encoding/base64"
	"fmt"
	"sync"
)

//go:embed weathercodes/*.png
var files embed.FS

//go:embed cascade/facefinder
var faceCascade []byte

// FaceCascade returns the pigo face-detection cascade.
func FaceCascade() []byte { return faceCascade }

var (
	mu    sync.Mutex
	cache = map[string]string{}
)

// WeatherIcon returns a data: URI for an icon stem such as "10000" (code+day flag).
// Unknown codes fall back to the generic "cloudy" icon so the layout never breaks.
func WeatherIcon(stem string) string {
	mu.Lock()
	defer mu.Unlock()
	if v, ok := cache[stem]; ok {
		return v
	}
	data, err := files.ReadFile(fmt.Sprintf("weathercodes/%s.png", stem))
	if err != nil {
		data, err = files.ReadFile("weathercodes/10010.png")
		if err != nil {
			return ""
		}
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	cache[stem] = uri
	return uri
}

// HasWeatherIcon reports whether an icon exists for the stem.
func HasWeatherIcon(stem string) bool {
	_, err := files.ReadFile(fmt.Sprintf("weathercodes/%s.png", stem))
	return err == nil
}
