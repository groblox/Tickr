package shared

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// DropboxClient handles API interaction with Dropbox.
type DropboxClient struct {
	AppKey       string
	RefreshToken string
	FilePath     string
	accessToken  string
}

// GetDropboxClient initializes a DropboxClient from environment variables.
func GetDropboxClient() *DropboxClient {
	appKey := os.Getenv("DROPBOX_APP_KEY")
	if appKey == "" {
		appKey = "vmj3ivdahewiqzu" // default client ID
	}
	return &DropboxClient{
		AppKey:       appKey,
		RefreshToken: os.Getenv("DROPBOX_REFRESH_TOKEN"),
		FilePath:     os.Getenv("DROPBOX_FILE_PATH"),
	}
}

// IsDropboxEnabled returns true if there are credentials in the environment to connect to Dropbox.
func IsDropboxEnabled() bool {
	return os.Getenv("DROPBOX_REFRESH_TOKEN") != "" || os.Getenv("DROPBOX_ACCESS_TOKEN") != ""
}

// GetToken returns the cached access token or refreshes it using the refresh token.
func (db *DropboxClient) GetToken() (string, error) {
	// If a static access token is provided, prefer that
	if token := os.Getenv("DROPBOX_ACCESS_TOKEN"); token != "" && db.accessToken == "" {
		db.accessToken = token
		return token, nil
	}

	if db.accessToken != "" {
		return db.accessToken, nil
	}

	if db.RefreshToken == "" {
		return "", fmt.Errorf("dropbox refresh token is not configured")
	}

	// POST to https://api.dropboxapi.com/oauth2/token
	val := url.Values{}
	val.Set("grant_type", "refresh_token")
	val.Set("refresh_token", db.RefreshToken)
	val.Set("client_id", db.AppKey)

	resp, err := http.PostForm("https://api.dropboxapi.com/oauth2/token", val)
	if err != nil {
		return "", fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to refresh token: status %d, body: %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	db.accessToken = res.AccessToken
	return db.accessToken, nil
}

// ResolveFilePath returns the configured Dropbox path or resolves it dynamically.
func (db *DropboxClient) ResolveFilePath() string {
	if db.FilePath != "" {
		return db.FilePath
	}
	path, err := db.FindJSONFile()
	if err == nil && path != "" {
		db.FilePath = path
		return path
	}
	db.FilePath = "/tasks.json"
	return "/tasks.json"
}

// FindJSONFile searches the App Folder for the first .json file.
func (db *DropboxClient) FindJSONFile() (string, error) {
	token, err := db.GetToken()
	if err != nil {
		return "", err
	}

	bodyJSON := []byte(`{"path": ""}`)
	req, err := http.NewRequest("POST", "https://api.dropboxapi.com/2/files/list_folder", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		db.accessToken = ""
		token, err = db.GetToken()
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Body = io.NopCloser(bytes.NewReader(bodyJSON))
		resp, err = client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list_folder failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path_lower"`
			Tag  string `json:".tag"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	for _, entry := range res.Entries {
		if entry.Tag == "file" && strings.HasSuffix(strings.ToLower(entry.Name), ".json") {
			return entry.Path, nil
		}
	}

	return "", fmt.Errorf("no json file found in Dropbox app folder")
}

// Download downloads file contents from Dropbox.
func (db *DropboxClient) Download() ([]byte, error) {
	token, err := db.GetToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://content.dropboxapi.com/2/files/download", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", fmt.Sprintf(`{"path": "%s"}`, db.FilePath))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		db.accessToken = ""
		token, err = db.GetToken()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
	}

	// Dropbox returns a 409 Conflict with path/not_found error for missing files
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("file_not_found")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// Upload uploads file contents to Dropbox.
func (db *DropboxClient) Upload(data []byte) error {
	token, err := db.GetToken()
	if err != nil {
		return err
	}

	arg := fmt.Sprintf(`{"path": "%s", "mode": "overwrite", "autorename": false, "mute": true, "strict_conflict": false}`, db.FilePath)

	req, err := http.NewRequest("POST", "https://content.dropboxapi.com/2/files/upload", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", arg)
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		db.accessToken = ""
		token, err = db.GetToken()
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Body = io.NopCloser(bytes.NewReader(data))
		resp, err = client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// ParseTasksFromJSON extracts a list of tasks from raw JSON bytes.
func ParseTasksFromJSON(data []byte) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}

	// 1. Try parsing as array of interface{}
	var arr []interface{}
	if err := json.Unmarshal(data, &arr); err == nil {
		return extractFromSlice(arr), nil
	}

	// 2. Try parsing as map[string]interface{}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err == nil {
		// Look for common keys: "tasks", "items", "data", "list"
		for _, key := range []string{"tasks", "items", "data", "list"} {
			if val, ok := obj[key]; ok {
				if subSlice, ok := val.([]interface{}); ok {
					return extractFromSlice(subSlice), nil
				}
			}
		}
		// Fallback: search for any array value
		for _, val := range obj {
			if subSlice, ok := val.([]interface{}); ok {
				return extractFromSlice(subSlice), nil
			}
		}
	}

	return nil, fmt.Errorf("could not parse JSON as array or map with array field")
}

func extractFromSlice(arr []interface{}) []string {
	var result []string
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		case map[string]interface{}:
			// Skip tasks marked as done
			if doneVal, hasDone := v["done"]; hasDone {
				if done, ok := doneVal.(bool); ok && done {
					continue
				}
			}
			if textVal, ok := v["text"]; ok {
				if textStr, ok := textVal.(string); ok {
					trimmed := strings.TrimSpace(textStr)
					if trimmed != "" {
						result = append(result, trimmed)
					}
				}
			}
		}
	}
	return result
}

// UpdateTasksInJSON modifies the JSON content with the new list of tasks while preserving structure and other fields.
func UpdateTasksInJSON(originalData []byte, newTasks []string) ([]byte, error) {
	if len(originalData) == 0 {
		// Initialize as array of maps with "text" field
		var list []map[string]interface{}
		for _, t := range newTasks {
			list = append(list, map[string]interface{}{"text": t})
		}
		return json.MarshalIndent(list, "", "  ")
	}

	// Try parsing as array
	var arr []interface{}
	if err := json.Unmarshal(originalData, &arr); err == nil {
		isStringArray := false
		if len(arr) > 0 {
			if _, ok := arr[0].(string); ok {
				isStringArray = true
			}
		}

		var newArr []interface{}
		if isStringArray {
			for _, t := range newTasks {
				newArr = append(newArr, t)
			}
		} else {
			// Find existing maps to preserve custom fields
			existingMaps := make(map[string]map[string]interface{})
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if textVal, ok := m["text"]; ok {
						if textStr, ok := textVal.(string); ok {
							existingMaps[textStr] = m
						}
					}
				}
			}
			for _, t := range newTasks {
				if existingMap, ok := existingMaps[t]; ok {
					newArr = append(newArr, existingMap)
				} else {
					newArr = append(newArr, map[string]interface{}{"text": t})
				}
			}
		}
		return json.MarshalIndent(newArr, "", "  ")
	}

	// Try parsing as map
	var obj map[string]interface{}
	if err := json.Unmarshal(originalData, &obj); err == nil {
		var tasksKey string
		var subSlice []interface{}
		for _, key := range []string{"tasks", "items", "data", "list"} {
			if val, ok := obj[key]; ok {
				if s, ok := val.([]interface{}); ok {
					tasksKey = key
					subSlice = s
					break
				}
			}
		}
		if tasksKey == "" {
			for key, val := range obj {
				if s, ok := val.([]interface{}); ok {
					tasksKey = key
					subSlice = s
					break
				}
			}
		}

		if tasksKey != "" {
			isStringArray := false
			if len(subSlice) > 0 {
				if _, ok := subSlice[0].(string); ok {
					isStringArray = true
				}
			}

			var newArr []interface{}
			if isStringArray {
				for _, t := range newTasks {
					newArr = append(newArr, t)
				}
			} else {
				existingMaps := make(map[string]map[string]interface{})
				for _, item := range subSlice {
					if m, ok := item.(map[string]interface{}); ok {
						if textVal, ok := m["text"]; ok {
							if textStr, ok := textVal.(string); ok {
								existingMaps[textStr] = m
							}
						}
					}
				}
				for _, t := range newTasks {
					if existingMap, ok := existingMaps[t]; ok {
						newArr = append(newArr, existingMap)
					} else {
						newArr = append(newArr, map[string]interface{}{"text": t})
					}
				}
			}
			obj[tasksKey] = newArr
			return json.MarshalIndent(obj, "", "  ")
		}
	}

	// Fallback: simple array of maps
	var list []map[string]interface{}
	for _, t := range newTasks {
		list = append(list, map[string]interface{}{"text": t})
	}
	return json.MarshalIndent(list, "", "  ")
}

// GetTasks fetches and parses tasks from Dropbox.
func (db *DropboxClient) GetTasks() ([]string, error) {
	filePath := db.ResolveFilePath()
	db.FilePath = filePath

	data, err := db.Download()
	if err != nil {
		if err.Error() == "file_not_found" {
			return []string{}, nil
		}
		return nil, err
	}

	return ParseTasksFromJSON(data)
}

// SaveTasks serializes and uploads the task list to Dropbox.
func (db *DropboxClient) SaveTasks(tasks []string) error {
	filePath := db.ResolveFilePath()
	db.FilePath = filePath

	var originalData []byte
	data, err := db.Download()
	if err == nil {
		originalData = data
	} else if err.Error() != "file_not_found" {
		return fmt.Errorf("failed to check existing file: %w", err)
	}

	updatedData, err := UpdateTasksInJSON(originalData, tasks)
	if err != nil {
		return fmt.Errorf("failed to update tasks JSON: %w", err)
	}

	if err := db.Upload(updatedData); err != nil {
		return fmt.Errorf("failed to upload tasks to dropbox: %w", err)
	}

	return nil
}

// GenerateVerifier generates a PKCE-compatible code verifier.
func GenerateVerifier() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateChallenge generates a PKCE-compatible code challenge from a verifier.
func GenerateChallenge(verifier string) string {
	h := sha256.New()
	h.Write([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// UpdateEnvFile updates a key-value pair in a .env file or creates it.
func UpdateEnvFile(path string, key, val string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(path, []byte(fmt.Sprintf("%s=%s\n", key, val)), 0600)
		}
		return err
	}

	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+"=") {
			lines[i] = fmt.Sprintf("%s=%s", key, val)
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, fmt.Sprintf("%s=%s", key, val))
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}
