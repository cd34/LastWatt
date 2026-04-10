package ecobee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/term"

	"github.com/mcd/lastwatt/internal/actions"
)

func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	return term.ReadPassword(fd)
}

const (
	apiBase       = "https://api.ecobee.com"
	authBase      = "https://auth.ecobee.com"
	thermostatURL = apiBase + "/1/thermostat"
	webClientID   = "183eORFPlXyz9BbDZwqexHPBQoVjgadh"
	redirectURI   = "https://www.ecobee.com/home/authCallback"
	authScopes    = "openid smartWrite piiWrite piiRead smartRead deleteGrants"
)

var (
	accessTokenRe = regexp.MustCompile(`name="access_token"\s+value="([^"]+)"`)
	expiresInRe   = regexp.MustCompile(`name="expires_in"\s+value="([^"]+)"`)
)

func init() {
	actions.Register(&authAction{})
	actions.Register(&readModeAction{})
	actions.Register(&setHoldAction{})
	actions.Register(&resumeAction{})
}

var apiClient = &http.Client{Timeout: 30 * time.Second}

// StartKeepAlive runs a background loop that proactively re-authenticates at
// the given interval, keeping the Auth0 session cookies from going stale.
func StartKeepAlive(ctx context.Context, interval time.Duration, store actions.StateStore, log *slog.Logger) {
	log.Info("ecobee keepalive starting", "interval", interval)

	// Use a timer so we can fire immediately on first tick
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("ecobee keepalive stopped")
			return
		case <-timer.C:
			keepAliveOnce(store, log)
			timer.Reset(interval)
		}
	}
}

func keepAliveOnce(store actions.StateStore, log *slog.Logger) {
	username, _ := store.Get("ecobee.username")
	password, _ := store.Get("ecobee.password")
	if username == "" || password == "" {
		log.Warn("ecobee keepalive: no stored credentials, skipping")
		return
	}

	mfaCallback := totpCallback(store)

	newToken, newExpires, err := authenticate(username, password, mfaCallback, store)
	if err != nil {
		log.Warn("ecobee keepalive session refresh failed", "error", err)
		return
	}

	expiry := time.Now().Add(time.Duration(newExpires) * time.Second)
	store.Set("ecobee.access_token", newToken)
	store.Set("ecobee.token_expires", expiry.Format(time.RFC3339))
	log.Info("ecobee keepalive refreshed token", "expires", expiry.Format(time.RFC3339))
}

// totpCallback returns an MFA callback that generates TOTP codes from the
// stored secret. Returns nil if no secret is configured.
func totpCallback(store actions.StateStore) MFACallback {
	secret, ok := store.Get("ecobee.totp_secret")
	if !ok || secret == "" {
		return nil
	}
	return func() (string, error) {
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			return "", fmt.Errorf("TOTP generation failed: %w", err)
		}
		return code, nil
	}
}

// MFACallback prompts for a TOTP code and returns it.
type MFACallback func() (string, error)

// savedCookie is the serializable form of an HTTP cookie.
type savedCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure"`
	HttpOnly bool      `json:"httponly"`
}

// saveCookies serializes the cookie jar for the auth domain to JSON.
func saveCookies(jar http.CookieJar) string {
	authURL, _ := url.Parse(authBase)
	cookies := jar.Cookies(authURL)
	saved := make([]savedCookie, 0, len(cookies))
	for _, c := range cookies {
		saved = append(saved, savedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	data, _ := json.Marshal(saved)
	return string(data)
}

// loadCookies restores cookies into a jar from JSON.
func loadCookies(jar http.CookieJar, data string) {
	if data == "" {
		return
	}
	var saved []savedCookie
	if err := json.Unmarshal([]byte(data), &saved); err != nil {
		return
	}
	authURL, _ := url.Parse(authBase)
	cookies := make([]*http.Cookie, 0, len(saved))
	for _, s := range saved {
		cookies = append(cookies, &http.Cookie{
			Name:     s.Name,
			Value:    s.Value,
			Domain:   s.Domain,
			Path:     s.Path,
			Expires:  s.Expires,
			Secure:   s.Secure,
			HttpOnly: s.HttpOnly,
		})
	}
	jar.SetCookies(authURL, cookies)
}

// authenticate performs the Auth0 web login flow and returns an access token.
// If savedJar cookies are provided, they're loaded into the jar to potentially skip MFA.
// Returns the cookie jar data to persist for future refreshes.
func authenticate(username, password string, mfaCallback MFACallback, store actions.StateStore) (token string, expiresIn int, err error) {
	jar, _ := cookiejar.New(nil)

	// Restore cookies from previous auth session (may skip MFA)
	if store != nil {
		if cookieData, ok := store.Get("ecobee.auth_cookies"); ok {
			loadCookies(jar, cookieData)
		}
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
	}

	// Step 1: Initiate Auth0 authorization — follow redirects, capture final URL
	authURL := fmt.Sprintf("%s/authorize?response_type=token&response_mode=form_post&client_id=%s&redirect_uri=%s&audience=%s&scope=%s",
		authBase,
		webClientID,
		url.QueryEscape(redirectURI),
		url.QueryEscape("https://prod.ecobee.com/api/v1"),
		url.QueryEscape(authScopes),
	)

	resp, err := client.Get(authURL)
	if err != nil {
		return "", 0, fmt.Errorf("auth step 1 (authorize): %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Check if cookies already gave us a token (fully cached session)
	html := string(body)
	if tokenMatch := accessTokenRe.FindStringSubmatch(html); len(tokenMatch) >= 2 {
		expiresIn = parseExpires(html)
		persistCookies(jar, store)
		return tokenMatch[1], expiresIn, nil
	}

	identifierURL := resp.Request.URL.String()

	// Step 2: Submit username (identifier step)
	formData := url.Values{"username": {username}}
	resp, err = client.PostForm(identifierURL, formData)
	if err != nil {
		return "", 0, fmt.Errorf("auth step 2 (username): %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	passwordURL := resp.Request.URL.String()

	// Step 3: Submit username + password
	formData = url.Values{
		"username": {username},
		"password": {password},
	}
	resp, err = client.PostForm(passwordURL, formData)
	if err != nil {
		return "", 0, fmt.Errorf("auth step 3 (password): %w", err)
	}

	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", 0, fmt.Errorf("auth step 3 (reading response): %w", err)
	}

	html = string(body)

	// Check if we got an MFA challenge page
	if strings.Contains(html, "otp") || strings.Contains(html, "mfa") ||
		strings.Contains(html, "one-time") || strings.Contains(html, "verification") ||
		strings.Contains(html, "authenticator") || strings.Contains(html, "code") {
		mfaURL := resp.Request.URL.String()

		if mfaCallback == nil {
			return "", 0, fmt.Errorf("MFA required but no callback provided — run 'lastwatt ecobee-auth' interactively")
		}
		code, err := mfaCallback()
		if err != nil {
			return "", 0, err
		}

		formData = url.Values{"code": {code}}
		resp, err = client.PostForm(mfaURL, formData)
		if err != nil {
			return "", 0, fmt.Errorf("auth MFA submit: %w", err)
		}

		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", 0, fmt.Errorf("auth MFA (reading response): %w", err)
		}
		html = string(body)
	}

	// Check for error indicators
	if strings.Contains(html, "Wrong email or password") || strings.Contains(html, "wrong-credentials") {
		return "", 0, fmt.Errorf("wrong email or password")
	}

	tokenMatch := accessTokenRe.FindStringSubmatch(html)
	if len(tokenMatch) < 2 {
		return "", 0, fmt.Errorf("could not extract access_token from auth response")
	}

	expiresIn = parseExpires(html)

	// Save cookies for future refreshes (skip MFA next time)
	persistCookies(jar, store)

	return tokenMatch[1], expiresIn, nil
}

func parseExpires(html string) int {
	expiresIn := 3600 // default 1h
	if m := expiresInRe.FindStringSubmatch(html); len(m) >= 2 {
		fmt.Sscanf(m[1], "%d", &expiresIn)
	}
	return expiresIn
}

func persistCookies(jar http.CookieJar, store actions.StateStore) {
	if store != nil {
		store.Set("ecobee.auth_cookies", saveCookies(jar))
	}
}

// getToken retrieves the token from state, re-authenticating if expired.
func getToken(store actions.StateStore) (string, error) {
	token, ok := store.Get("ecobee.access_token")
	if !ok {
		return "", fmt.Errorf("ecobee not authenticated — run 'lastwatt ecobee-auth' first")
	}

	// Check if token needs refresh
	expiresStr, _ := store.Get("ecobee.token_expires")
	if expiresStr != "" {
		expires, err := time.Parse(time.RFC3339, expiresStr)
		if err == nil && time.Now().After(expires.Add(-5*time.Minute)) {
			// Re-authenticate using stored credentials + cookies
			username, _ := store.Get("ecobee.username")
			password, _ := store.Get("ecobee.password")
			if username == "" || password == "" {
				return "", fmt.Errorf("ecobee credentials missing — run 'lastwatt ecobee-auth' again")
			}
			newToken, newExpires, err := authenticate(username, password, totpCallback(store), store)
			if err != nil {
				return "", fmt.Errorf("token refresh failed: %w", err)
			}
			expiry := time.Now().Add(time.Duration(newExpires) * time.Second)
			store.Set("ecobee.access_token", newToken)
			store.Set("ecobee.token_expires", expiry.Format(time.RFC3339))
			return newToken, nil
		}
	}

	return token, nil
}

// apiResult holds the response from an Ecobee API call.
type apiResult struct {
	Body       []byte
	StatusCode int
}

func apiRequest(ctx context.Context, method, endpoint string, token string, body any) (*apiResult, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")

	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &apiResult{Body: respBody, StatusCode: resp.StatusCode}, nil
}

// apiRequestWithRefresh wraps apiRequest and retries once on expired token (HTTP 500 or 401).
func apiRequestWithRefresh(ctx context.Context, method, endpoint string, store actions.StateStore, body any) ([]byte, error) {
	token, err := getToken(store)
	if err != nil {
		return nil, err
	}

	result, err := apiRequest(ctx, method, endpoint, token, body)
	if err != nil {
		return nil, err
	}

	// Retry on auth-related failures
	if result.StatusCode == http.StatusUnauthorized || result.StatusCode == http.StatusInternalServerError {
		username, _ := store.Get("ecobee.username")
		password, _ := store.Get("ecobee.password")
		if username != "" && password != "" {
			newToken, newExpires, authErr := authenticate(username, password, totpCallback(store), store)
			if authErr == nil {
				expiry := time.Now().Add(time.Duration(newExpires) * time.Second)
				store.Set("ecobee.access_token", newToken)
				store.Set("ecobee.token_expires", expiry.Format(time.RFC3339))
				result, err = apiRequest(ctx, method, endpoint, newToken, body)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if result.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ecobee API %d: %s", result.StatusCode, string(result.Body))
	}

	return result.Body, nil
}

// authAction handles the Ecobee web-based authentication flow.
type authAction struct{}

func (a *authAction) Name() string                  { return "ecobee.auth" }
func (a *authAction) Validate(map[string]any) error { return nil }

func (a *authAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	var username string

	fmt.Print("Enter your Ecobee email: ")
	fmt.Scanln(&username)

	fmt.Print("Enter your Ecobee password: ")
	passwordBytes, err := readPassword()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	password := string(passwordBytes)
	fmt.Println()

	fmt.Println("Authenticating...")

	// Use stored TOTP secret if available, otherwise prompt
	var mfaCallback MFACallback
	if cb := totpCallback(store); cb != nil {
		mfaCallback = func() (string, error) {
			code, err := cb()
			if err == nil {
				fmt.Println("MFA code generated automatically from stored TOTP secret.")
			}
			return code, err
		}
	} else {
		mfaCallback = func() (string, error) {
			var code string
			fmt.Print("Enter MFA code from authenticator app: ")
			fmt.Scanln(&code)
			return code, nil
		}
	}

	token, expiresIn, err := authenticate(username, password, mfaCallback, store)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	expiry := time.Now().Add(time.Duration(expiresIn) * time.Second)
	store.Set("ecobee.access_token", token)
	store.Set("ecobee.token_expires", expiry.Format(time.RFC3339))
	store.Set("ecobee.username", username)
	store.Set("ecobee.password", password)

	// Prompt for TOTP secret to enable automatic MFA
	if _, ok := store.Get("ecobee.totp_secret"); !ok {
		fmt.Print("Enter TOTP secret for automatic MFA (or press Enter to skip): ")
		var secret string
		fmt.Scanln(&secret)
		if secret != "" {
			// Validate the secret by generating a code
			if _, err := totp.GenerateCode(secret, time.Now()); err != nil {
				fmt.Printf("Warning: invalid TOTP secret (%v), not saved.\n", err)
			} else {
				store.Set("ecobee.totp_secret", secret)
				fmt.Println("TOTP secret saved — daemon can now handle MFA automatically.")
			}
		}
	}

	fmt.Printf("Ecobee authentication successful! Token expires in %dh.\n", expiresIn/3600)
	return nil
}

// readModeAction reads and saves the current thermostat mode.
type readModeAction struct{}

func (a *readModeAction) Name() string                  { return "ecobee.read_mode" }
func (a *readModeAction) Validate(map[string]any) error { return nil }

func (a *readModeAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	sel := url.Values{
		"json": {`{"selection":{"selectionType":"registered","selectionMatch":"","includeRuntime":true,"includeSettings":true}}`},
	}
	endpoint := thermostatURL + "?" + sel.Encode()

	body, err := apiRequestWithRefresh(ctx, http.MethodGet, endpoint, store, nil)
	if err != nil {
		return fmt.Errorf("ecobee.read_mode: %w", err)
	}

	var result struct {
		ThermostatList []struct {
			Settings struct {
				HvacMode string `json:"hvacMode"`
			} `json:"settings"`
			Runtime struct {
				DesiredHeat       int `json:"desiredHeat"`
				DesiredCool       int `json:"desiredCool"`
				ActualTemperature int `json:"actualTemperature"`
			} `json:"runtime"`
		} `json:"thermostatList"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("ecobee.read_mode parse: %w", err)
	}

	if len(result.ThermostatList) == 0 {
		return fmt.Errorf("no thermostats found")
	}

	t := result.ThermostatList[0]
	store.Set("ecobee.saved_mode", t.Settings.HvacMode)
	store.Set("ecobee.saved_heat", fmt.Sprintf("%d", t.Runtime.DesiredHeat))
	store.Set("ecobee.saved_cool", fmt.Sprintf("%d", t.Runtime.DesiredCool))

	fmt.Printf("Thermostat mode: %s, heat: %.1f°F, cool: %.1f°F, current: %.1f°F\n",
		t.Settings.HvacMode,
		float64(t.Runtime.DesiredHeat)/10,
		float64(t.Runtime.DesiredCool)/10,
		float64(t.Runtime.ActualTemperature)/10,
	)

	return nil
}

// setHoldAction sets a temperature hold on the thermostat.
type setHoldAction struct{}

func (a *setHoldAction) Name() string { return "ecobee.set_hold" }

func (a *setHoldAction) Validate(params map[string]any) error {
	if _, ok := params["heat_temp"]; !ok {
		return fmt.Errorf("missing required param: heat_temp")
	}
	if _, ok := params["cool_temp"]; !ok {
		return fmt.Errorf("missing required param: cool_temp")
	}
	return nil
}

func (a *setHoldAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	// Ecobee uses temp * 10 (e.g., 550 = 55.0°F)
	heatTemp := toInt(params["heat_temp"]) * 10
	coolTemp := toInt(params["cool_temp"]) * 10

	body := map[string]any{
		"selection": map[string]any{
			"selectionType":  "registered",
			"selectionMatch": "",
		},
		"functions": []map[string]any{
			{
				"type": "setHold",
				"params": map[string]any{
					"holdType":     "indefinite",
					"coolHoldTemp": coolTemp,
					"heatHoldTemp": heatTemp,
				},
			},
		},
	}

	_, err := apiRequestWithRefresh(ctx, http.MethodPost, thermostatURL, store, body)
	if err != nil {
		return fmt.Errorf("ecobee.set_hold: %w", err)
	}

	fmt.Printf("Set hold: heat=%.1f°F, cool=%.1f°F\n", float64(heatTemp)/10, float64(coolTemp)/10)
	return nil
}

// resumeAction removes any holds and resumes the normal schedule.
type resumeAction struct{}

func (a *resumeAction) Name() string                  { return "ecobee.resume" }
func (a *resumeAction) Validate(map[string]any) error { return nil }

func (a *resumeAction) Execute(ctx context.Context, params map[string]any, store actions.StateStore) error {
	body := map[string]any{
		"selection": map[string]any{
			"selectionType":  "registered",
			"selectionMatch": "",
		},
		"functions": []map[string]any{
			{
				"type": "resumeProgram",
				"params": map[string]any{
					"resumeAll": true,
				},
			},
		},
	}

	_, err := apiRequestWithRefresh(ctx, http.MethodPost, thermostatURL, store, body)
	if err != nil {
		return fmt.Errorf("ecobee.resume: %w", err)
	}

	fmt.Println("Resumed thermostat program")
	return nil
}

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		var n int
		fmt.Sscanf(val, "%d", &n)
		return n
	default:
		return 0
	}
}
