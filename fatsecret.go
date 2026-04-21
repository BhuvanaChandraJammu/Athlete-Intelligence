package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ============================================================
// FATSECRET
// ============================================================

type NutritionData struct {
	Calories float64        `json:"calories"`
	Protein  float64        `json:"protein"`
	Carbs    float64        `json:"carbs"`
	Fat      float64        `json:"fat"`
	Meals    []MealEntry    `json:"meals"`
	LastMeal *time.Time     `json:"last_meal"`
}

type MealEntry struct {
	Name     string    `json:"name"`
	Calories float64   `json:"calories"`
	Protein  float64   `json:"protein"`
	Carbs    float64   `json:"carbs"`
	Fat      float64   `json:"fat"`
	Time     time.Time `json:"time"`
}

func fetchTodayNutrition(ctx context.Context) (*NutritionData, error) {
	if fatSecretKey == "" {
		return &NutritionData{}, nil
	}

	// FatSecret uses OAuth 1.0
	// Get today's food diary
	now := time.Now()
	dateInt := int(now.Unix() / 86400) // FatSecret date format

	params := map[string]string{
		"method":                 "food_entries.get_month",
		"date":                   fmt.Sprintf("%d", dateInt),
		"format":                 "json",
		"oauth_consumer_key":     fatSecretKey,
		"oauth_nonce":            fmt.Sprintf("%d", now.UnixNano()),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        fmt.Sprintf("%d", now.Unix()),
		"oauth_version":          "1.0",
	}

	signature := generateOAuth1Signature("GET", "https://platform.fatsecret.com/rest/server.api", params, fatSecretSecret)
	params["oauth_signature"] = signature

	// Build query string
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	queryParts := make([]string, 0, len(params))
	for _, k := range keys {
		queryParts = append(queryParts, url.QueryEscape(k)+"="+url.QueryEscape(params[k]))
	}
	queryString := strings.Join(queryParts, "&")

	apiURL := "https://platform.fatsecret.com/rest/server.api?" + queryString
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&raw)

	nutrition := &NutritionData{}

	// Parse food entries
	if foodEntries, ok := raw["food_entries"].(map[string]interface{}); ok {
		if entries, ok := foodEntries["food_entry"].([]interface{}); ok {
			for _, e := range entries {
				entry, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				meal := MealEntry{}
				meal.Name = fmt.Sprintf("%v", entry["food_entry_name"])
				if cal, ok := entry["calories"].(float64); ok {
					meal.Calories = cal
					nutrition.Calories += cal
				}
				if p, ok := entry["protein"].(float64); ok {
					meal.Protein = p
					nutrition.Protein += p
				}
				if c, ok := entry["carbohydrate"].(float64); ok {
					meal.Carbs = c
					nutrition.Carbs += c
				}
				if f, ok := entry["fat"].(float64); ok {
					meal.Fat = f
					nutrition.Fat += f
				}
				nutrition.Meals = append(nutrition.Meals, meal)
			}
		}
	}

	return nutrition, nil
}

func generateOAuth1Signature(method, apiURL string, params map[string]string, secret string) string {
	// Sort params
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	paramParts := make([]string, 0, len(params))
	for _, k := range keys {
		paramParts = append(paramParts, url.QueryEscape(k)+"="+url.QueryEscape(params[k]))
	}
	paramString := strings.Join(paramParts, "&")

	baseString := method + "&" + url.QueryEscape(apiURL) + "&" + url.QueryEscape(paramString)
	signingKey := url.QueryEscape(secret) + "&"

	mac := hmac.New(sha1.New, []byte(signingKey))
	mac.Write([]byte(baseString))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func calculateNutritionTargets(whoopData map[string]interface{}) map[string]interface{} {
	weightKg := 80.0 // default
	caloriesBurned := 2500.0

	if whoopData != nil {
		if body, ok := whoopData["body"].(map[string]interface{}); ok {
			if w, ok := body["weight_kilogram"].(float64); ok {
				weightKg = w
			}
		}
		if cycles, ok := whoopData["cycles"].([]interface{}); ok && len(cycles) > 0 {
			if cy, ok := cycles[0].(map[string]interface{}); ok {
				if score, ok := cy["score"].(map[string]interface{}); ok {
					if kj, ok := score["kilojoule"].(float64); ok {
						caloriesBurned = kj * 0.239
					}
				}
			}
		}
	}

	// Body recomp targets
	targetCalories := caloriesBurned - 300 // slight deficit for recomp
	protein := weightKg * 2.2
	fat := weightKg * 0.9
	carbCalories := targetCalories - (protein * 4) - (fat * 9)
	carbs := carbCalories / 4
	if carbs < 100 {
		carbs = 100
	}

	return map[string]interface{}{
		"calories":        int(targetCalories),
		"protein":         int(protein),
		"carbs":           int(carbs),
		"fat":             int(fat),
		"calories_burned": int(caloriesBurned),
	}
}
