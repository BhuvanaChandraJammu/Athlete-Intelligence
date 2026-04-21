package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	whoopClientID     = getEnv("WHOOP_CLIENT_ID", "")
	whoopClientSecret = getEnv("WHOOP_CLIENT_SECRET", "")
	spotifyClientID   = getEnv("SPOTIFY_CLIENT_ID", "")
	spotifyClientSecret = getEnv("SPOTIFY_CLIENT_SECRET", "")
	fatSecretKey      = getEnv("FATSECRET_CONSUMER_KEY", "")
	fatSecretSecret   = getEnv("FATSECRET_CONSUMER_SECRET", "")
	openWeatherKey    = getEnv("OPENWEATHER_API_KEY", "")
	googleClientID    = getEnv("GOOGLE_CLIENT_ID", "")
	googleClientSecret = getEnv("GOOGLE_CLIENT_SECRET", "")
	anthropicKey      = getEnv("ANTHROPIC_API_KEY", "")
	city              = getEnv("CITY", "Southfield")
	cityCountry       = getEnv("CITY_COUNTRY", "US")
	baseURL           = getEnv("BASE_URL", "https://athlete-intelligence.up.railway.app")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type TokenStore struct {
	WhoopAccessToken  string    `json:"whoop_access_token"`
	WhoopRefreshToken string    `json:"whoop_refresh_token"`
	WhoopExpiry       time.Time `json:"whoop_expiry"`
	SpotifyAccessToken  string  `json:"spotify_access_token"`
	SpotifyRefreshToken string  `json:"spotify_refresh_token"`
	SpotifyExpiry       time.Time `json:"spotify_expiry"`
	GoogleAccessToken  string    `json:"google_access_token"`
	GoogleRefreshToken string    `json:"google_refresh_token"`
	GoogleExpiry       time.Time `json:"google_expiry"`
}

var tokenStore = &TokenStore{}
var tokenFile = "tokens.json"

func loadTokens() {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, tokenStore)
}

func saveTokens() {
	data, _ := json.MarshalIndent(tokenStore, "", "  ")
	os.WriteFile(tokenFile, data, 0600)
}

func main() {
	loadTokens()

	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Main dashboard
	mux.HandleFunc("/", handleDashboard)

	// Auth routes
	mux.HandleFunc("/whoop/login", handleWhoopLogin)
	mux.HandleFunc("/whoop/callback", handleWhoopCallback)
	mux.HandleFunc("/spotify/login", handleSpotifyLogin)
	mux.HandleFunc("/spotify/callback", handleSpotifyCallback)
	mux.HandleFunc("/google/login", handleGoogleLogin)
	mux.HandleFunc("/google/callback", handleGoogleCallback)

	// API routes
	mux.HandleFunc("/api/dashboard", handleAPIDashboard)
	mux.HandleFunc("/api/insights", handleAPIInsights)
	mux.HandleFunc("/api/workout-plan", handleAPIWorkoutPlan)
	mux.HandleFunc("/api/nutrition", handleAPINutrition)
	mux.HandleFunc("/api/sleep", handleAPISleep)
	mux.HandleFunc("/api/playlist", handleAPIPlaylist)

	port := getEnv("PORT", "8080")
	log.Printf("🚀 Athlete Intelligence running on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "static/index.html")
}

func handleAPIDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	result := map[string]interface{}{}

	// Whoop data
	if tokenStore.WhoopAccessToken != "" {
		if time.Now().After(tokenStore.WhoopExpiry) {
			refreshWhoopToken()
		}
		whoopData, err := fetchWhoopDashboard(ctx, tokenStore.WhoopAccessToken)
		if err == nil {
			result["whoop"] = whoopData
		} else {
			result["whoop_error"] = err.Error()
		}
	} else {
		result["whoop_connected"] = false
	}

	// Weather data
	weather, err := fetchWeather(ctx)
	if err == nil {
		result["weather"] = weather
	}

	// FatSecret data
	nutrition, err := fetchTodayNutrition(ctx)
	if err == nil {
		result["nutrition"] = nutrition
	}

	// Google Calendar
	if tokenStore.GoogleAccessToken != "" {
		if time.Now().After(tokenStore.GoogleExpiry) {
			refreshGoogleToken()
		}
		events, err := fetchCalendarEvents(ctx, tokenStore.GoogleAccessToken)
		if err == nil {
			result["calendar"] = events
		}
	}

	// Spotify
	if tokenStore.SpotifyAccessToken != "" {
		result["spotify_connected"] = true
	}

	json.NewEncoder(w).Encode(result)
}

func handleAPIInsights(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var whoopData map[string]interface{}
	if tokenStore.WhoopAccessToken != "" {
		whoopData, _ = fetchWhoopDashboard(ctx, tokenStore.WhoopAccessToken)
	}

	weather, _ := fetchWeather(ctx)
	nutrition, _ := fetchTodayNutrition(ctx)

	insights, err := generateInsights(ctx, whoopData, weather, nutrition)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"insights": insights,
	})
}

func handleAPIWorkoutPlan(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var whoopData map[string]interface{}
	if tokenStore.WhoopAccessToken != "" {
		whoopData, _ = fetchWhoopDashboard(ctx, tokenStore.WhoopAccessToken)
	}

	weather, _ := fetchWeather(ctx)
	nutrition, _ := fetchTodayNutrition(ctx)

	var calendarEvents []CalendarEvent
	if tokenStore.GoogleAccessToken != "" {
		calendarEvents, _ = fetchCalendarEvents(ctx, tokenStore.GoogleAccessToken)
	}

	plan := generateWorkoutPlan(whoopData, weather, nutrition, calendarEvents)
	json.NewEncoder(w).Encode(plan)
}

func handleAPINutrition(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	nutrition, err := fetchTodayNutrition(ctx)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}

	var whoopData map[string]interface{}
	if tokenStore.WhoopAccessToken != "" {
		whoopData, _ = fetchWhoopDashboard(ctx, tokenStore.WhoopAccessToken)
	}

	targets := calculateNutritionTargets(whoopData)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logged":  nutrition,
		"targets": targets,
	})
}

func handleAPISleep(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var whoopData map[string]interface{}
	if tokenStore.WhoopAccessToken != "" {
		whoopData, _ = fetchWhoopDashboard(ctx, tokenStore.WhoopAccessToken)
	}

	var calendarEvents []CalendarEvent
	if tokenStore.GoogleAccessToken != "" {
		calendarEvents, _ = fetchCalendarEvents(ctx, tokenStore.GoogleAccessToken)
	}

	sleepPlan := generateSleepPlan(whoopData, calendarEvents)
	json.NewEncoder(w).Encode(sleepPlan)
}

func handleAPIPlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	workoutType := r.URL.Query().Get("type")
	if workoutType == "" {
		workoutType = "strength"
	}

	if tokenStore.SpotifyAccessToken == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Spotify not connected",
		})
		return
	}

	if time.Now().After(tokenStore.SpotifyExpiry) {
		refreshSpotifyToken()
	}

	playlist, err := generateWorkoutPlaylist(ctx, tokenStore.SpotifyAccessToken, workoutType)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(playlist)
}

func generateInsights(ctx context.Context, whoopData map[string]interface{}, weather *WeatherData, nutrition *NutritionData) (string, error) {
	if anthropicKey == "" {
		return "Connect your Anthropic API key for AI insights.", nil
	}

	recoveryScore := 0
	hrv := 0
	rhr := 0
	sleepScore := 0
	strain := 0.0

	if whoopData != nil {
		if rec, ok := whoopData["recovery"].(map[string]interface{}); ok {
			if score, ok := rec["recovery_score"].(float64); ok {
				recoveryScore = int(score)
			}
			if h, ok := rec["hrv_rmssd_milli"].(float64); ok {
				hrv = int(h)
			}
			if r, ok := rec["resting_heart_rate"].(float64); ok {
				rhr = int(r)
			}
		}
		if sl, ok := whoopData["sleep"].(map[string]interface{}); ok {
			if s, ok := sl["sleep_performance_percentage"].(float64); ok {
				sleepScore = int(s)
			}
		}
		if cy, ok := whoopData["strain"].(float64); ok {
			strain = cy
		}
	}

	weatherDesc := "unknown"
	temp := 0.0
	if weather != nil {
		weatherDesc = weather.Description
		temp = weather.Temp
	}

	calConsumed := 0
proteinConsumed := 0.0
if nutrition != nil {
    calConsumed = int(nutrition.Calories)
    proteinConsumed = float64(nutrition.Protein)
}

	prompt := fmt.Sprintf(`You are an elite sports scientist and personal fitness coach. Analyze this athlete's data and provide 5 specific, actionable insights.

LIVE DATA:
- Recovery Score: %d%%
- HRV: %dms
- Resting HR: %dbpm
- Sleep Score: %d%%
- Today's Strain: %.1f/21
- Weather: %s, %.0f°F
- Calories consumed today: %d
- Protein consumed today: %.0fg

GOALS: Body recomposition - athletic/lean physique + strength gains.

Give 5 insights as JSON array. Each has:
- "title": 4-6 words
- "text": 2-3 sentences referencing actual numbers
- "action": exact thing to do right now
- "priority": "HIGH" | "MEDIUM" | "LOW"
- "category": "RECOVERY" | "TRAINING" | "NUTRITION" | "SLEEP" | "PERFORMANCE"

Return ONLY the JSON array.`,
		recoveryScore, hrv, rhr, sleepScore, strain,
		weatherDesc, temp, calConsumed, proteinConsumed)

	return callClaude(ctx, prompt)
}

func callClaude(ctx context.Context, prompt string) (string, error) {
	payload := map[string]interface{}{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1500,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", 
		jsonReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", anthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
		if item, ok := content[0].(map[string]interface{}); ok {
			if text, ok := item["text"].(string); ok {
				return text, nil
			}
		}
	}

	return "", fmt.Errorf("failed to get Claude response")
}
