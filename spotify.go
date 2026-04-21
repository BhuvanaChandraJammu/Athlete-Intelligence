package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ============================================================
// SPOTIFY
// ============================================================

func handleSpotifyLogin(w http.ResponseWriter, r *http.Request) {
	scopes := "user-read-private user-read-email playlist-modify-public playlist-modify-private user-top-read user-library-read"
	authURL := fmt.Sprintf(
		"https://accounts.spotify.com/authorize?response_type=code&client_id=%s&scope=%s&redirect_uri=%s",
		spotifyClientID,
		url.QueryEscape(scopes),
		url.QueryEscape(baseURL+"/spotify/callback"),
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func handleSpotifyCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "No code received", 400)
		return
	}

	tokens, err := exchangeSpotifyCode(code)
	if err != nil {
		http.Error(w, "Failed: "+err.Error(), 500)
		return
	}

	tokenStore.SpotifyAccessToken = tokens["access_token"].(string)
	if rt, ok := tokens["refresh_token"].(string); ok {
		tokenStore.SpotifyRefreshToken = rt
	}
	tokenStore.SpotifyExpiry = time.Now().Add(55 * time.Minute)
	saveTokens()

	http.Redirect(w, r, "/?connected=spotify", http.StatusTemporaryRedirect)
}

func exchangeSpotifyCode(code string) (map[string]interface{}, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", baseURL+"/spotify/callback")

	req, _ := http.NewRequest("POST", "https://accounts.spotify.com/api/token", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(spotifyClientID+":"+spotifyClientSecret)))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func refreshSpotifyToken() error {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tokenStore.SpotifyRefreshToken)

	req, _ := http.NewRequest("POST", "https://accounts.spotify.com/api/token", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(spotifyClientID+":"+spotifyClientSecret)))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var tokens map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&tokens)

	if at, ok := tokens["access_token"].(string); ok {
		tokenStore.SpotifyAccessToken = at
		tokenStore.SpotifyExpiry = time.Now().Add(55 * time.Minute)
		saveTokens()
	}
	return nil
}

type PlaylistData struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Tracks      []TrackInfo   `json:"tracks"`
	PlaylistURL string        `json:"playlist_url"`
	Phase       string        `json:"phase"`
}

type TrackInfo struct {
	Name    string `json:"name"`
	Artist  string `json:"artist"`
	URI     string `json:"uri"`
	BPM     int    `json:"bpm"`
}

func generateWorkoutPlaylist(ctx context.Context, token, workoutType string) (*PlaylistData, error) {
	// Get user's top tracks
	req, _ := http.NewRequestWithContext(ctx, "GET",
		"https://api.spotify.com/v1/me/top/tracks?limit=50&time_range=medium_term", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var topTracks map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&topTracks)

	tracks := []TrackInfo{}
	if items, ok := topTracks["items"].([]interface{}); ok {
		for _, item := range items {
			track, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			ti := TrackInfo{}
			ti.Name = fmt.Sprintf("%v", track["name"])
			ti.URI = fmt.Sprintf("%v", track["uri"])
			if artists, ok := track["artists"].([]interface{}); ok && len(artists) > 0 {
				if artist, ok := artists[0].(map[string]interface{}); ok {
					ti.Artist = fmt.Sprintf("%v", artist["name"])
				}
			}
			tracks = append(tracks, ti)
		}
	}

	// Get user ID
	userReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.spotify.com/v1/me", nil)
	userReq.Header.Set("Authorization", "Bearer "+token)
	userResp, err := client.Do(userReq)
	if err != nil {
		return &PlaylistData{Name: "Workout Mix", Tracks: tracks}, nil
	}
	defer userResp.Body.Close()

	var userInfo map[string]interface{}
	json.NewDecoder(userResp.Body).Decode(&userInfo)
	userID := fmt.Sprintf("%v", userInfo["id"])

	// Create playlist
	playlistName := "Athlete Intelligence — " + strings.Title(workoutType) + " " + time.Now().Format("Jan 2")
	playlistDesc := "Auto-generated by Athlete Intelligence based on your training"

	createBody := map[string]interface{}{
		"name":        playlistName,
		"description": playlistDesc,
		"public":      false,
	}
	createData, _ := json.Marshal(createBody)

	createReq, _ := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("https://api.spotify.com/v1/users/%s/playlists", userID),
		strings.NewReader(string(createData)))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(createReq)
	if err != nil {
		return &PlaylistData{Name: playlistName, Tracks: tracks}, nil
	}
	defer createResp.Body.Close()

	var playlist map[string]interface{}
	json.NewDecoder(createResp.Body).Decode(&playlist)

	playlistID := fmt.Sprintf("%v", playlist["id"])
	playlistURL := ""
	if ext, ok := playlist["external_urls"].(map[string]interface{}); ok {
		playlistURL = fmt.Sprintf("%v", ext["spotify"])
	}

	// Add tracks to playlist
	if len(tracks) > 0 {
		uris := make([]string, 0, len(tracks))
		for _, t := range tracks {
			if t.URI != "" && t.URI != "<nil>" {
				uris = append(uris, t.URI)
			}
		}

		if len(uris) > 0 {
			addBody := map[string]interface{}{"uris": uris}
			addData, _ := json.Marshal(addBody)
			addReq, _ := http.NewRequestWithContext(ctx, "POST",
				fmt.Sprintf("https://api.spotify.com/v1/playlists/%s/tracks", playlistID),
				strings.NewReader(string(addData)))
			addReq.Header.Set("Authorization", "Bearer "+token)
			addReq.Header.Set("Content-Type", "application/json")
			client.Do(addReq)
		}
	}

	return &PlaylistData{
		Name:        playlistName,
		Description: playlistDesc,
		Tracks:      tracks,
		PlaylistURL: playlistURL,
		Phase:       workoutType,
	}, nil
}
