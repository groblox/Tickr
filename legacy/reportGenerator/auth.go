package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"shared"
)

// runCLIAuth runs the interactive console-based Dropbox OAuth authentication flow.
func runCLIAuth() {
	fmt.Println("==================================================")
	fmt.Println("     Breaklist Dropbox CLI Authentication")
	fmt.Println("==================================================")

	verifier, err := shared.GenerateVerifier()
	if err != nil {
		fmt.Printf("Error generating PKCE verifier: %v\n", err)
		return
	}

	challenge := shared.GenerateChallenge(verifier)

	appKey := os.Getenv("DROPBOX_APP_KEY")
	if appKey == "" {
		appKey = "vmj3ivdahewiqzu"
	}

	redirectURI := "http://localhost:3030/auth/dropbox/callback"
	authURL := fmt.Sprintf(
		"https://www.dropbox.com/oauth2/authorize?client_id=%s&response_type=code&code_challenge_method=S256&code_challenge=%s&redirect_uri=%s&token_access_type=offline",
		url.QueryEscape(appKey), url.QueryEscape(challenge), url.QueryEscape(redirectURI),
	)

	fmt.Println("\nStarting a temporary authentication server on port 3030...")
	fmt.Println("Please log in to your browser at this URL to link your Dropbox account:")
	fmt.Println(authURL)
	fmt.Println("\nWaiting for browser redirect...")

	// Open the browser automatically
	openBrowser(authURL)

	// Start a local HTTP server to receive the authorization code redirect
	srv := &http.Server{Addr: ":3030"}

	http.HandleFunc("/auth/dropbox/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			fmt.Fprintln(w, "Error: Missing authorization code in redirect.")
			fmt.Println("Redirect received, but missing authorization code.")
			return
		}

		// Exchange code for token
		val := url.Values{}
		val.Set("grant_type", "authorization_code")
		val.Set("code", code)
		val.Set("client_id", appKey)
		val.Set("code_verifier", verifier)
		val.Set("redirect_uri", redirectURI)

		resp, err := http.PostForm("https://api.dropboxapi.com/oauth2/token", val)
		if err != nil {
			msg := fmt.Sprintf("Error exchanging code: %v", err)
			fmt.Fprintln(w, msg)
			fmt.Println(msg)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			msg := fmt.Sprintf("Error exchanging code: status %s, body: %s", resp.Status, string(body))
			fmt.Fprintln(w, msg)
			fmt.Println(msg)
			return
		}

		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			fmt.Fprintln(w, "Error: Failed to decode response from Dropbox.")
			fmt.Println("Failed to decode token response.")
			return
		}

		// Update .env file
		_ = shared.UpdateEnvFile(".env", "DROPBOX_REFRESH_TOKEN", tokenResp.RefreshToken)
		_ = shared.UpdateEnvFile("../.env", "DROPBOX_REFRESH_TOKEN", tokenResp.RefreshToken)

		// Set in process environment
		os.Setenv("DROPBOX_REFRESH_TOKEN", tokenResp.RefreshToken)

		// Write response page to the user browser
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `
			<!DOCTYPE html>
			<html>
			<head>
				<title>Success!</title>
				<style>
					body { font-family: sans-serif; text-align: center; margin-top: 100px; background-color: #f7f9fa; }
					.card { background: white; padding: 40px; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.08); display: inline-block; }
					h1 { color: #0061FE; }
				</style>
			</head>
			<body>
				<div class="card">
					<h1>Success!</h1>
					<p>Your Dropbox account has been successfully linked to Breaklist.</p>
					<p>You can close this tab and return to your terminal.</p>
				</div>
			</body>
			</html>
		`)

		fmt.Println("\nSuccessfully retrieved refresh token from Dropbox and updated .env!")
		fmt.Println("Shutting down authentication server...")

		// Shutdown the server gracefully in the background
		go func() {
			_ = srv.Shutdown(context.Background())
		}()
	})

	// Wait for server shutdown or handle port errors
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP server ListenAndServe error: %v\n", err)
	}
}

// openBrowser opens the specified URL in the default browser of the OS.
func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", etc.
		cmd = "xdg-open"
		args = []string{url}
	}

	_ = exec.Command(cmd, args...).Start()
}
