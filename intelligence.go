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

// ============================================================
// GOOGLE CALENDAR
// ============================================================

type CalendarEvent struct {
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Duration int       `json:"duration_minutes"`
}

func handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	scopes := "https://www.googleapis.com/auth/calendar.readonly"
	redirectURI := baseURL + "/google/callback"
	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&access_type=offline&prompt=consent",
		googleClientID,
		url.QueryEscape(redirectURI),
		url.QueryEscape(scopes),
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error")
		http.Error(w, "No code received. Error: "+errMsg, 400)
		return
	}

	tokens, err := exchangeGoogleCode(code)
	if err != nil {
		http.Error(w, "Failed: "+err.Error(), 500)
		return
	}

	if at, ok := tokens["access_token"].(string); ok {
		tokenStore.GoogleAccessToken = at
	}
	if rt, ok := tokens["refresh_token"].(string); ok {
		tokenStore.GoogleRefreshToken = rt
	}
	tokenStore.GoogleExpiry = time.Now().Add(55 * time.Minute)
	saveTokens()

	http.Redirect(w, r, "/?connected=google", http.StatusTemporaryRedirect)
}

func exchangeGoogleCode(code string) (map[string]interface{}, error) {
	redirectURI := baseURL + "/google/callback"
	data := url.Values{}
	data.Set("code", code)
	data.Set("client_id", googleClientID)
	data.Set("client_secret", googleClientSecret)
	data.Set("redirect_uri", redirectURI)
	data.Set("grant_type", "authorization_code")

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func refreshGoogleToken() error {
	data := url.Values{}
	data.Set("refresh_token", tokenStore.GoogleRefreshToken)
	data.Set("client_id", googleClientID)
	data.Set("client_secret", googleClientSecret)
	data.Set("grant_type", "refresh_token")

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var tokens map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&tokens)

	if at, ok := tokens["access_token"].(string); ok {
		tokenStore.GoogleAccessToken = at
		tokenStore.GoogleExpiry = time.Now().Add(55 * time.Minute)
		saveTokens()
	}
	return nil
}

func fetchCalendarEvents(ctx context.Context, token string) ([]CalendarEvent, error) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)

	apiURL := fmt.Sprintf(
		"https://www.googleapis.com/calendar/v3/calendars/primary/events?timeMin=%s&timeMax=%s&singleEvents=true&orderBy=startTime",
		url.QueryEscape(now.Format(time.RFC3339)),
		url.QueryEscape(tomorrow.Format(time.RFC3339)),
	)

	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&raw)

	events := []CalendarEvent{}
	if items, ok := raw["items"].([]interface{}); ok {
		for _, item := range items {
			ev, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			event := CalendarEvent{}
			event.Title = fmt.Sprintf("%v", ev["summary"])

			if start, ok := ev["start"].(map[string]interface{}); ok {
				if dt, ok := start["dateTime"].(string); ok {
					event.Start, _ = time.Parse(time.RFC3339, dt)
				}
			}
			if end, ok := ev["end"].(map[string]interface{}); ok {
				if dt, ok := end["dateTime"].(string); ok {
					event.End, _ = time.Parse(time.RFC3339, dt)
				}
			}
			event.Duration = int(event.End.Sub(event.Start).Minutes())
			events = append(events, event)
		}
	}
	return events, nil
}

// ============================================================
// INTELLIGENCE ENGINE
// ============================================================

type WorkoutPlan struct {
	Recommended    bool       `json:"recommended"`
	BestWindow     string     `json:"best_window"`
	Reason         string     `json:"reason"`
	Intensity      string     `json:"intensity"`
	Focus          string     `json:"focus"`
	Exercises      []Exercise `json:"exercises"`
	PreWorkoutMeal string     `json:"pre_workout_meal"`
	PostWorkoutMeal string    `json:"post_workout_meal"`
	PreWorkoutTime string     `json:"pre_workout_time"`
	PostWorkoutTime string    `json:"post_workout_time"`
	Warnings       []string   `json:"warnings"`
	WeatherNote    string     `json:"weather_note"`
}

type Exercise struct {
	Name        string `json:"name"`
	Sets        string `json:"sets"`
	Reps        string `json:"reps"`
	Tip         string `json:"tip"`
	MuscleGroup string `json:"muscle_group"`
}

type SleepPlan struct {
	RecommendedBedtime  string              `json:"recommended_bedtime"`
	RecommendedWakeTime string              `json:"recommended_wake_time"`
	TargetDuration      string              `json:"target_duration"`
	WindDownTime        string              `json:"wind_down_time"`
	LastMealTime        string              `json:"last_meal_time"`
	Reason              string              `json:"reason"`
	Schedule            []SleepScheduleItem `json:"schedule"`
}

type SleepScheduleItem struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	Color  string `json:"color"`
}

func generateWorkoutPlan(whoopData map[string]interface{}, weather *WeatherData, nutrition *NutritionData, events []CalendarEvent) *WorkoutPlan {
	plan := &WorkoutPlan{}

	recovery := 0
	strain := 0.0
	if whoopData != nil {
		if rec, ok := whoopData["recovery"].(map[string]interface{}); ok {
			if s, ok := rec["recovery_score"].(float64); ok {
				recovery = int(s)
			}
		}
		if s, ok := whoopData["strain"].(float64); ok {
			strain = s
		}
	}

	if recovery >= 67 {
		plan.Intensity = "HIGH"
		plan.Recommended = true
	} else if recovery >= 34 {
		plan.Intensity = "MODERATE"
		plan.Recommended = true
	} else {
		plan.Intensity = "REST"
		plan.Recommended = false
		plan.Reason = fmt.Sprintf("Recovery is only %d%% — rest or light mobility today to protect your HRV.", recovery)
	}

	plan.BestWindow = findBestWorkoutWindow(events)

	if weather != nil {
		plan.WeatherNote = weather.Recommendation
		if !weather.IsGoodForOutdoor {
			plan.Warnings = append(plan.Warnings, "Bad weather — indoor workout only today")
		}
	}

	plan.PreWorkoutMeal = "40g carbs + 20g protein (rice cakes + whey shake or banana + Greek yogurt)"
	plan.PostWorkoutMeal = "40g fast carbs + 35g protein (whey shake + banana)"
	plan.PreWorkoutTime = "90 minutes before your workout"
	plan.PostWorkoutTime = "Within 30 minutes after workout"

	trainedMuscles := map[string]time.Time{}
	if whoopData != nil {
		if workouts, ok := whoopData["workouts"].([]interface{}); ok {
			trainedMuscles = getTrainedMuscles(workouts)
		}
	}

	focus, exercises, warnings := selectMuscleGroup(trainedMuscles, recovery, strain)
	plan.Focus = focus
	plan.Exercises = exercises
	plan.Warnings = append(plan.Warnings, warnings...)

	if plan.Reason == "" {
		plan.Reason = fmt.Sprintf("Recovery %d%% — %s intensity session recommended. Focus: %s", recovery, strings.ToLower(plan.Intensity), focus)
	}

	return plan
}

func findBestWorkoutWindow(events []CalendarEvent) string {
	if len(events) == 0 {
		return "6:00 PM — 7:30 PM (no meetings found, evening slot suggested)"
	}

	now := time.Now()
	checkTimes := []time.Time{
		time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, now.Location()),
		time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location()),
		time.Date(now.Year(), now.Month(), now.Day(), 17, 0, 0, 0, now.Location()),
		time.Date(now.Year(), now.Month(), now.Day(), 19, 0, 0, 0, now.Location()),
	}

	for _, slot := range checkTimes {
		if slot.Before(now) {
			continue
		}
		slotEnd := slot.Add(90 * time.Minute)
		available := true
		for _, event := range events {
			if event.Start.Before(slotEnd) && event.End.After(slot) {
				available = false
				break
			}
		}
		if available {
			return slot.Format("3:04 PM") + " — " + slotEnd.Format("3:04 PM")
		}
	}

	return "No clear windows today — consider a shorter 45-min session or reschedule"
}

func selectMuscleGroup(trained map[string]time.Time, recovery int, strain float64) (string, []Exercise, []string) {
	warnings := []string{}
	now := time.Now()
	minRestHours := 48.0

	restedMuscles := []string{}
	allMuscles := []string{"chest", "back", "legs", "shoulders", "arms"}

	for _, muscle := range allMuscles {
		if lastTrained, ok := trained[muscle]; ok {
			hoursSince := now.Sub(lastTrained).Hours()
			if hoursSince >= minRestHours {
				restedMuscles = append(restedMuscles, muscle)
			} else {
				warnings = append(warnings, fmt.Sprintf("⚠️ %s trained %.0f hours ago — skip today", strings.Title(muscle), hoursSince))
			}
		} else {
			restedMuscles = append(restedMuscles, muscle)
		}
	}

	priority := []string{"legs", "back", "chest", "shoulders", "arms"}
	focus := "Full Body"
	for _, p := range priority {
		for _, r := range restedMuscles {
			if p == r {
				focus = strings.Title(p)
				break
			}
		}
		if focus != "Full Body" {
			break
		}
	}

	exercises := getExercisesForMuscle(focus, recovery)
	return focus, exercises, warnings
}

func getExercisesForMuscle(muscle string, recovery int) []Exercise {
	isHigh := recovery >= 67

	exerciseMap := map[string][]Exercise{
		"Legs": {
			{Name: "Barbell Back Squat", Sets: "5", Reps: ifStr(isHigh, "5", "8-10"), Tip: ifStr(isHigh, "Add 2.5kg if last session felt strong", "Moderate weight, focus on form"), MuscleGroup: "Quads/Glutes"},
			{Name: "Romanian Deadlift", Sets: "4", Reps: "8-10", Tip: "3-second eccentric, full stretch at bottom", MuscleGroup: "Hamstrings"},
			{Name: "Bulgarian Split Squat", Sets: "3", Reps: "10 each", Tip: "Best recomp movement — go as heavy as form allows", MuscleGroup: "Quads/Glutes"},
			{Name: "Hip Thrust", Sets: "3", Reps: "12", Tip: "Squeeze at top for 1 second", MuscleGroup: "Glutes"},
			{Name: "Calf Raises", Sets: "4", Reps: "20", Tip: "Full range — don't bounce", MuscleGroup: "Calves"},
		},
		"Back": {
			{Name: "Deadlift", Sets: "4", Reps: ifStr(isHigh, "5", "8"), Tip: ifStr(isHigh, "Add 2.5kg if last set felt clean", "Moderate weight today"), MuscleGroup: "Full Back"},
			{Name: "Barbell Row", Sets: "4", Reps: "8-10", Tip: "Pull to lower chest, squeeze lats", MuscleGroup: "Lats/Rhomboids"},
			{Name: "Pull-ups / Lat Pulldown", Sets: "3", Reps: "8-12", Tip: "Full stretch at top, drive elbows down", MuscleGroup: "Lats"},
			{Name: "Seated Cable Row", Sets: "3", Reps: "12", Tip: "Chest up, don't lean back excessively", MuscleGroup: "Mid Back"},
			{Name: "Face Pulls", Sets: "3", Reps: "15", Tip: "Crucial for shoulder health and posture", MuscleGroup: "Rear Delts"},
		},
		"Chest": {
			{Name: "Barbell Bench Press", Sets: "4", Reps: ifStr(isHigh, "5-6", "8-10"), Tip: ifStr(isHigh, "Try adding 2.5kg today", "Control the weight"), MuscleGroup: "Chest"},
			{Name: "Incline Dumbbell Press", Sets: "3", Reps: "8-12", Tip: "Upper chest is often undertrained", MuscleGroup: "Upper Chest"},
			{Name: "Cable Flys", Sets: "3", Reps: "12-15", Tip: "Full stretch at bottom, squeeze at top", MuscleGroup: "Chest"},
			{Name: "Dips", Sets: "3", Reps: "10-12", Tip: "Lean forward slightly for chest emphasis", MuscleGroup: "Lower Chest/Triceps"},
		},
		"Shoulders": {
			{Name: "Overhead Press", Sets: "4", Reps: ifStr(isHigh, "6-8", "10-12"), Tip: "Don't flare elbows — keep them slightly forward", MuscleGroup: "Front/Side Delts"},
			{Name: "Lateral Raises", Sets: "4", Reps: "15", Tip: "Slight forward lean, lead with elbows", MuscleGroup: "Side Delts"},
			{Name: "Rear Delt Flys", Sets: "3", Reps: "15", Tip: "Essential for shoulder balance and health", MuscleGroup: "Rear Delts"},
			{Name: "Arnold Press", Sets: "3", Reps: "12", Tip: "Full rotation hits all delt heads", MuscleGroup: "All Delts"},
		},
		"Arms": {
			{Name: "Barbell Curl", Sets: "4", Reps: "8-12", Tip: "Full extension at bottom, squeeze at top", MuscleGroup: "Biceps"},
			{Name: "Skull Crushers", Sets: "4", Reps: "10-12", Tip: "Keep elbows pointed up throughout", MuscleGroup: "Triceps"},
			{Name: "Hammer Curls", Sets: "3", Reps: "12 each", Tip: "Builds brachialis for thicker arms", MuscleGroup: "Biceps/Brachialis"},
			{Name: "Tricep Pushdown", Sets: "3", Reps: "15", Tip: "Lock elbows at sides, extend fully", MuscleGroup: "Triceps"},
		},
		"Full Body": {
			{Name: "Goblet Squat", Sets: "3", Reps: "15", Tip: "Light to moderate weight", MuscleGroup: "Legs"},
			{Name: "Push-ups", Sets: "3", Reps: "15-20", Tip: "Full range of motion", MuscleGroup: "Chest"},
			{Name: "Dumbbell Row", Sets: "3", Reps: "12 each", Tip: "Keep back flat", MuscleGroup: "Back"},
			{Name: "Walking Lunges", Sets: "3", Reps: "20 steps", Tip: "Controlled tempo", MuscleGroup: "Legs"},
		},
	}

	if ex, ok := exerciseMap[muscle]; ok {
		return ex
	}
	return exerciseMap["Full Body"]
}

func generateSleepPlan(whoopData map[string]interface{}, events []CalendarEvent) *SleepPlan {
	plan := &SleepPlan{}

	sleepNeed := 8.0 * 60

	if whoopData != nil {
		if strain, ok := whoopData["strain"].(float64); ok {
			if strain > 14 {
				sleepNeed += 30
			} else if strain > 10 {
				sleepNeed += 15
			}
		}
	}

	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)
	wakeTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 6, 30, 0, 0, now.Location())

	for _, event := range events {
		if event.Start.After(tomorrow.Add(-2*time.Hour)) && event.Start.Hour() < 9 {
			wakeTime = event.Start.Add(-90 * time.Minute)
		}
	}

	bedtime := wakeTime.Add(-time.Duration(sleepNeed) * time.Minute)

	plan.RecommendedBedtime = bedtime.Format("3:04 PM")
	plan.RecommendedWakeTime = wakeTime.Format("3:04 PM")
	plan.TargetDuration = fmt.Sprintf("%.0fh %02.0fm", sleepNeed/60, float64(int(sleepNeed)%60))
	plan.WindDownTime = bedtime.Add(-30 * time.Minute).Format("3:04 PM")
	plan.LastMealTime = bedtime.Add(-90 * time.Minute).Format("3:04 PM")
	plan.Reason = fmt.Sprintf("Targeting %.0f hours to optimize recovery and HRV", sleepNeed/60)

	plan.Schedule = []SleepScheduleItem{
		{Time: bedtime.Add(-90 * time.Minute).Format("3:04 PM"), Action: "Last meal — no food after this", Color: "#ff9f43"},
		{Time: bedtime.Add(-60 * time.Minute).Format("3:04 PM"), Action: "Stop caffeine completely", Color: "#ff6b35"},
		{Time: bedtime.Add(-30 * time.Minute).Format("3:04 PM"), Action: "Dim lights, reduce screen brightness", Color: "#f5c400"},
		{Time: bedtime.Add(-15 * time.Minute).Format("3:04 PM"), Action: "Wind down — stretch or read, no phone", Color: "#7c6fff"},
		{Time: plan.RecommendedBedtime, Action: "Sleep target — 8hrs before wake time", Color: "#4ecdc4"},
		{Time: plan.RecommendedWakeTime, Action: "Wake target — consistent schedule boosts HRV", Color: "#00f5a0"},
	}

	return plan
}

func ifStr(condition bool, a, b string) string {
	if condition {
		return a
	}
	return b
}
