package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ============================================================
// WEATHER
// ============================================================

type WeatherData struct {
	Temp        float64 `json:"temp"`
	FeelsLike   float64 `json:"feels_like"`
	Description string  `json:"description"`
	Main        string  `json:"main"`
	Humidity    int     `json:"humidity"`
	WindSpeed   float64 `json:"wind_speed"`
	IsGoodForOutdoor bool `json:"is_good_for_outdoor"`
	Recommendation string `json:"recommendation"`
}

func fetchWeather(ctx context.Context) (*WeatherData, error) {
	url := fmt.Sprintf(
		"https://api.openweathermap.org/data/2.5/weather?q=%s,%s&appid=%s&units=imperial",
		city, cityCountry, openWeatherKey,
	)

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&raw)

	weather := &WeatherData{}

	if main, ok := raw["main"].(map[string]interface{}); ok {
		if t, ok := main["temp"].(float64); ok {
			weather.Temp = t
		}
		if f, ok := main["feels_like"].(float64); ok {
			weather.FeelsLike = f
		}
		if h, ok := main["humidity"].(float64); ok {
			weather.Humidity = int(h)
		}
	}

	if wList, ok := raw["weather"].([]interface{}); ok && len(wList) > 0 {
		if w, ok := wList[0].(map[string]interface{}); ok {
			weather.Description = fmt.Sprintf("%v", w["description"])
			weather.Main = fmt.Sprintf("%v", w["main"])
		}
	}

	if wind, ok := raw["wind"].(map[string]interface{}); ok {
		if s, ok := wind["speed"].(float64); ok {
			weather.WindSpeed = s
		}
	}

	// Determine if good for outdoor workout
	badConditions := []string{"Rain", "Drizzle", "Thunderstorm", "Snow", "Tornado", "Hurricane"}
	weather.IsGoodForOutdoor = true
	for _, bad := range badConditions {
		if weather.Main == bad {
			weather.IsGoodForOutdoor = false
			break
		}
	}
	if weather.Temp > 95 || weather.Temp < 20 {
		weather.IsGoodForOutdoor = false
	}

	if weather.IsGoodForOutdoor {
		weather.Recommendation = fmt.Sprintf("Great weather for outdoor training! %.0f°F and %s.", weather.Temp, weather.Description)
	} else {
		weather.Recommendation = fmt.Sprintf("%.0f°F with %s — stick to indoor training today.", weather.Temp, weather.Description)
	}

	return weather, nil
}
