package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const whoopBaseURL = "https://api.prod.whoop.com"

func handleWhoopLogin(w http.ResponseWriter, r *http.Request) {
	state := fmt.Sprintf("%d", time.Now().UnixNano())
	authURL := fmt.Sprintf(
		"%s/oauth/oauth2/auth?response_type=code&client_id=%s&redirect_uri=%s/whoop/callback&scope=%s&state=%s",
		whoopBaseURL,
		whoopClientID,
		url.QueryEscape(baseURL),
		url.QueryEscape("read:recovery read:sleep read:strain read:workout read:cycles read:profile read:body_measurement"),
		state,
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func handleWhoopCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "No code received", 400)
		return
	}

	tokens, err := exchangeWhoopCode(code)
	if err != nil {
		http.Error(w, "Failed to exchange code: "+err.Error(), 500)
		return
	}

	tokenStore.WhoopAccessToken = tokens["access_token"].(string)
	if rt, ok := tokens["refresh_token"].(string); ok {
		tokenStore.WhoopRefreshToken = rt
	}
	expiresIn := int(tokens["expires_in"].(float64))
	tokenStore.WhoopExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	saveTokens()

	http.Redirect(w, r, "/?connected=whoop", http.StatusTemporaryRedirect)
}

func exchangeWhoopCode(code string) (map[string]interface{}, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", baseURL+"/whoop/callback")
	data.Set("client_id", whoopClientID)
	data.Set("client_secret", whoopClientSecret)

	resp, err := http.PostForm(whoopBaseURL+"/oauth/oauth2/token", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func refreshWhoopToken() error {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tokenStore.WhoopRefreshToken)
	data.Set("client_id", whoopClientID)
	data.Set("client_secret", whoopClientSecret)

	resp, err := http.PostForm(whoopBaseURL+"/oauth/oauth2/token", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var tokens map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&tokens)

	if at, ok := tokens["access_token"].(string); ok {
		tokenStore.WhoopAccessToken = at
	}
	if rt, ok := tokens["refresh_token"].(string); ok {
		tokenStore.WhoopRefreshToken = rt
	}
	if ei, ok := tokens["expires_in"].(float64); ok {
		tokenStore.WhoopExpiry = time.Now().Add(time.Duration(ei) * time.Second)
	}
	saveTokens()
	return nil
}

type WhoopDashboard struct {
	Recovery  map[string]interface{} `json:"recovery"`
	Sleep     map[string]interface{} `json:"sleep"`
	Strain    float64                `json:"strain"`
	Cycles    []map[string]interface{} `json:"cycles"`
	Workouts  []map[string]interface{} `json:"workouts"`
	Profile   map[string]interface{} `json:"profile"`
	Body      map[string]interface{} `json:"body"`
}

func fetchWhoopDashboard(ctx context.Context, token string) (map[string]interface{}, error) {
	result := map[string]interface{}{}

	// Fetch recovery
	recovery, err := whoopGet(ctx, token, "/developer/v2/recovery?limit=1")
	if err == nil {
		if records, ok := recovery["records"].([]interface{}); ok && len(records) > 0 {
			if rec, ok := records[0].(map[string]interface{}); ok {
				if score, ok := rec["score"].(map[string]interface{}); ok {
					result["recovery"] = score
				}
			}
		}
	}

	// Fetch sleep
	sleep, err := whoopGet(ctx, token, "/developer/v2/activity/sleep?limit=1")
	if err == nil {
		if records, ok := sleep["records"].([]interface{}); ok && len(records) > 0 {
			if sl, ok := records[0].(map[string]interface{}); ok {
				if score, ok := sl["score"].(map[string]interface{}); ok {
					result["sleep"] = score
					// Get sleep times
					result["sleep_start"] = sl["start"]
					result["sleep_end"] = sl["end"]
				}
			}
		}
	}

	// Fetch cycles (7 days)
	cycles, err := whoopGet(ctx, token, "/developer/v2/cycle?limit=7")
	if err == nil {
		result["cycles"] = cycles["records"]
		// Get today's strain
		if records, ok := cycles["records"].([]interface{}); ok && len(records) > 0 {
			if cy, ok := records[0].(map[string]interface{}); ok {
				if score, ok := cy["score"].(map[string]interface{}); ok {
					if strain, ok := score["strain"].(float64); ok {
						result["strain"] = strain
					}
				}
			}
		}
	}

	// Fetch workouts
	workouts, err := whoopGet(ctx, token, "/developer/v2/activity/workout?limit=10")
	if err == nil {
		result["workouts"] = workouts["records"]
	}

	// Fetch profile
	profile, err := whoopGet(ctx, token, "/developer/v2/user/profile/basic")
	if err == nil {
		result["profile"] = profile
	}

	// Fetch body measurements
	body, err := whoopGet(ctx, token, "/developer/v2/user/measurement/body")
	if err == nil {
		result["body"] = body
	}

	return result, nil
}

func whoopGet(ctx context.Context, token, path string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", whoopBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("whoop API error: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

// Helper to get last trained muscle groups from workout history
func getTrainedMuscles(workouts []interface{}) map[string]time.Time {
	muscles := map[string]time.Time{}
	muscleMap := map[string][]string{
		"chest":     {"bench", "chest", "fly", "push"},
		"back":      {"deadlift", "row", "pullup", "lat", "back"},
		"legs":      {"squat", "leg", "lunge", "hip", "glute", "hamstring", "quad", "calf"},
		"shoulders": {"shoulder", "press", "lateral", "delt"},
		"arms":      {"curl", "tricep", "bicep", "arm"},
		"core":      {"core", "ab", "plank", "crunch"},
	}

	for _, w := range workouts {
		workout, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		sportName := strings.ToLower(fmt.Sprintf("%v", workout["sport_name"]))
		startStr := fmt.Sprintf("%v", workout["start"])
		startTime, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			continue
		}

		for muscle, keywords := range muscleMap {
			for _, kw := range keywords {
				if strings.Contains(sportName, kw) {
					if last, exists := muscles[muscle]; !exists || startTime.After(last) {
						muscles[muscle] = startTime
					}
				}
			}
		}
	}
	return muscles
}

func jsonReader(data []byte) *strings.Reader {
	return strings.NewReader(string(data))
}
