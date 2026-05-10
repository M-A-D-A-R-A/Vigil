//go:build smoke

package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"vigil/internal/app"
	"vigil/internal/config"
)

func TestFirstRunAndExplorerSmoke(t *testing.T) {
	chromePath, ok := findChrome()
	if !ok {
		t.Skip("Chrome or Chromium not found; set VIGIL_CHROME_PATH to run browser smoke tests")
	}

	testApp, err := app.New(context.Background(), config.Config{
		Addr:            ":0",
		DataDir:         t.TempDir(),
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	defer testApp.Close()

	server := httptest.NewServer(testApp.Handler)
	defer server.Close()

	browserCtx, cancel := newBrowserContext(t, chromePath)
	defer cancel()

	projectName := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	var ingestKey string
	var location string

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(server.URL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open app in browser: %v", err)
	}

	var initialHTML string
	if err := chromedp.Run(browserCtx,
		chromedp.Sleep(500*time.Millisecond),
		chromedp.OuterHTML("html", &initialHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("capture initial app HTML: %v", err)
	}
	if !strings.Contains(initialHTML, `data-testid="project-name-input"`) {
		t.Fatalf("first-run project input did not render; initial HTML:\n%s", initialHTML)
	}

	createCtx, cancelCreate := context.WithTimeout(browserCtx, 10*time.Second)
	defer cancelCreate()

	if err := chromedp.Run(createCtx,
		chromedp.WaitVisible(`[data-testid="project-name-input"]`, chromedp.ByQuery),
		chromedp.SendKeys(`[data-testid="project-name-input"]`, projectName, chromedp.ByQuery),
		chromedp.Click(`[data-testid="create-project-button"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit project create form in browser: %v", err)
	}

	if err := chromedp.Run(createCtx,
		waitLocationContains("/projects/"),
		chromedp.Location(&location),
		chromedp.WaitVisible(`[data-testid="latest-ingest-key"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="latest-ingest-key"]`, &ingestKey, chromedp.ByQuery),
	); err != nil {
		var html string
		var currentLocation string
		if captureErr := chromedp.Run(browserCtx,
			chromedp.Location(&currentLocation),
			chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		); captureErr == nil {
			t.Logf("browser location before failure: %s", currentLocation)
			t.Logf("rendered HTML before failure:\n%s", html)
		}
		t.Fatalf("create project in browser: %v", err)
	}

	projectID, err := projectIDFromLocation(location)
	if err != nil {
		t.Fatalf("read project id from %q: %v", location, err)
	}
	eventTS, err := timestampInsideLocationRange(location)
	if err != nil {
		t.Fatalf("read explorer time range from %q: %v", location, err)
	}
	ingestKey = strings.TrimSpace(ingestKey)
	if ingestKey == "" {
		t.Fatal("expected first-run UI to render an ingest key")
	}

	eventName := "browser.smoke.completed"
	postIngest(t, server.URL, ingestKey, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "trace",
		"ts":             eventTS.Format(time.RFC3339),
		"source":         "browser-smoke",
		"trace_id":       "trace-browser-smoke",
		"span_id":        "span-browser-smoke",
		"level":          "info",
		"name":           eventName,
		"attrs": map[string]any{
			"route": "/first-run",
		},
		"body": map[string]any{
			"message": "browser smoke event arrived",
		},
	})
	waitForIndexedLog(t, server.URL, projectID, 1)

	verifyCtx, cancelVerify := context.WithTimeout(browserCtx, 15*time.Second)
	defer cancelVerify()

	if err := chromedp.Run(verifyCtx,
		chromedp.Click(`[data-testid="refresh-current-tab"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="logs-table-shell"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`//*[contains(., "browser smoke event arrived")]`, chromedp.BySearch),
		chromedp.Click(`[data-testid="tab-traces"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`//*[contains(., "trace-browser-smoke")]`, chromedp.BySearch),
		chromedp.Click(`[data-testid="tab-stats"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="stats-total-events"]`, chromedp.ByQuery),
		waitText(`[data-testid="stats-total-events"]`, "1"),
	); err != nil {
		var html string
		var currentLocation string
		if captureErr := chromedp.Run(browserCtx,
			chromedp.Location(&currentLocation),
			chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		); captureErr == nil {
			t.Logf("browser location before explorer failure: %s", currentLocation)
			t.Logf("rendered HTML before explorer failure:\n%s", html)
		}
		t.Fatalf("verify explorer in browser: %v", err)
	}
}

func newBrowserContext(t *testing.T, chromePath string) (context.Context, context.CancelFunc) {
	t.Helper()

	parent, cancelParent := context.WithTimeout(context.Background(), 60*time.Second)
	userDataDir := t.TempDir()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(userDataDir),
		chromedp.Headless,
		chromedp.NoSandbox,
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	ctx, cancelBrowser := chromedp.NewContext(allocCtx)

	return ctx, func() {
		cancelBrowser()
		cancelAlloc()
		cancelParent()
	}
}

func findChrome() (string, bool) {
	if configured := strings.TrimSpace(os.Getenv("VIGIL_CHROME_PATH")); configured != "" {
		if isExecutable(configured) {
			return configured, true
		}
		return "", false
	}

	candidates := []string{
		"google-chrome",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
	}
	if runtime.GOOS == "darwin" {
		candidates = append([]string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}, candidates...)
	}

	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if isExecutable(candidate) {
				return candidate, true
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

func waitLocationContains(fragment string) chromedp.Action {
	return chromedp.PollFunction(
		`fragment => window.location.pathname.includes(fragment)`,
		nil,
		chromedp.WithPollingArgs(fragment),
		chromedp.WithPollingInterval(100*time.Millisecond),
		chromedp.WithPollingTimeout(5*time.Second),
	)
}

func waitText(selector, want string) chromedp.Action {
	return chromedp.PollFunction(
		`(selector, want) => {
			const element = document.querySelector(selector);
			return !!element && element.textContent.trim() === want;
		}`,
		nil,
		chromedp.WithPollingArgs(selector, want),
		chromedp.WithPollingInterval(100*time.Millisecond),
		chromedp.WithPollingTimeout(5*time.Second),
	)
}

func projectIDFromLocation(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "projects" {
		return "", fmt.Errorf("unexpected project route")
	}
	return parts[1], nil
}

func timestampInsideLocationRange(raw string) (time.Time, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return time.Time{}, err
	}
	toRaw := strings.TrimSpace(parsed.Query().Get("to"))
	if toRaw == "" {
		return time.Time{}, fmt.Errorf("missing to query parameter")
	}
	to, err := time.Parse(time.RFC3339Nano, toRaw)
	if err != nil {
		return time.Time{}, err
	}
	return to.UTC().Add(-time.Second), nil
}

func postIngest(t *testing.T, baseURL, key string, payload map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 from ingest, got %d", resp.StatusCode)
	}
}

func waitForIndexedLog(t *testing.T, baseURL, projectID string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var result struct {
			Total int `json:"total"`
		}
		getJSON(t, baseURL+"/api/logs?project_id="+url.QueryEscape(projectID), &result)
		if result.Total == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d indexed log(s)", want)
}

func getJSON(t *testing.T, requestURL string, target any) {
	t.Helper()
	resp, err := http.Get(requestURL)
	if err != nil {
		t.Fatalf("GET %s: %v", requestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d", requestURL, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode %s: %v", requestURL, err)
	}
}
