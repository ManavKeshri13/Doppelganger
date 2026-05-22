// browser.go — Project Doppelgänger  (Phase 2)
//
// Headless browser automation engine built on chromedp.
//
// RunBrowsingSession executes a list of search queries inside a hidden
// Chrome instance, mimicking human behaviour with randomised delays and
// natural interaction patterns.  It returns the number of queries that
// were successfully injected.

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/chromedp/chromedp"
)

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

// RunBrowsingSession launches a headless Chrome instance, runs every query in
// the supplied slice, and returns how many were completed without error.
func RunBrowsingSession(cfg *Config, queries []string) (int, error) {
	log.Printf("[BROWSER] Starting session with %d queries (headless=%v)", len(queries), cfg.BrowserHeadless)

	// --- 1. Build chromedp allocator options ---
	allocOpts := chromedp.DefaultExecAllocatorOptions[:]

	if cfg.BrowserHeadless {
		// Keep the default headless flags (already included)
	} else {
		// Remove the headless flag so a visible window appears (dev mode)
		allocOpts = removeHeadlessFlags(allocOpts)
	}

	// If a real Chrome profile dir is configured, attach to it so that
	// all visited URLs land in the user's actual browser history.
	// NOTE: Chrome cannot run a profile in headless mode while it is also
	// open normally, so we force non-headless when a profile is supplied.
	if cfg.ChromeUserDataDir != "" {
		log.Printf("[BROWSER] Using real Chrome profile: %s", cfg.ChromeUserDataDir)
		allocOpts = removeHeadlessFlags(allocOpts) // force visible / non-headless
		allocOpts = append(allocOpts,
			chromedp.UserDataDir(cfg.ChromeUserDataDir),
			chromedp.Flag("profile-directory", "Default"),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("no-default-browser-check", true),
		)
	}

	// Add extra stealth / realistic browser fingerprint flags
	allocOpts = append(allocOpts,
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
		chromedp.UserAgent(randomUserAgent()),
		chromedp.WindowSize(1366, 768),
	)

	// --- 2. Create the allocator context (manages the Chrome process) ---
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	// --- 3. Create the browser context ---
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(log.Printf),
	)
	defer cancelBrowser()

	// --- 4. Set a generous outer timeout for the whole session ---
	sessionCtx, cancelSession := context.WithTimeout(browserCtx, 10*time.Minute)
	defer cancelSession()

	// --- 5. Execute each query ---
	injected := 0
	for i, q := range queries {
		log.Printf("[BROWSER] Query %d/%d: %q", i+1, len(queries), q)

		if err := runSingleQuery(sessionCtx, q); err != nil {
			log.Printf("[BROWSER] ⚠️  Query %d failed: %v – continuing", i+1, err)
			// Non-fatal: continue with remaining queries
			continue
		}

		injected++

		// Human-like pause between queries (3–8 seconds)
		if i < len(queries)-1 {
			pause := humanPause(3*time.Second, 8*time.Second)
			log.Printf("[BROWSER] 💤 Waiting %v before next query...", pause.Round(time.Millisecond))
			select {
			case <-sessionCtx.Done():
				log.Println("[BROWSER] Context cancelled – stopping early")
				return injected, nil
			case <-time.After(pause):
			}
		}
	}

	log.Printf("[BROWSER] ✅ Session finished – %d/%d queries injected", injected, len(queries))
	return injected, nil
}

// ---------------------------------------------------------------------------
// Single-query execution
// ---------------------------------------------------------------------------

// runSingleQuery performs one complete search-and-click sequence:
//  1. Navigate to DuckDuckGo
//  2. Type the query with human-paced keystrokes
//  3. Submit and wait for the results page
//  4. Click the first organic result
//  5. Scroll the result page to simulate reading
func runSingleQuery(ctx context.Context, query string) error {
	// Per-query timeout (90 s is generous enough for slow connections)
	qCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// ---- Navigate ----
	if err := chromedp.Run(qCtx,
		chromedp.Navigate("https://duckduckgo.com/"),
		chromedp.WaitVisible(`input[name="q"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}

	humanPauseSync(500*time.Millisecond, 1500*time.Millisecond)

	// ---- Type query (character by character with random delays) ----
	if err := chromedp.Run(qCtx,
		chromedp.Click(`input[name="q"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("click search box: %w", err)
	}

	if err := typeHumanLike(qCtx, `input[name="q"]`, query); err != nil {
		return fmt.Errorf("type query: %w", err)
	}

	humanPauseSync(300*time.Millisecond, 800*time.Millisecond)

	// ---- Submit ----
	if err := chromedp.Run(qCtx,
		chromedp.Submit(`input[name="q"]`, chromedp.ByQuery),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("submit: %w", err)
	}

	humanPauseSync(1500*time.Millisecond, 3000*time.Millisecond)

	// ---- Click first organic result ----
	// DuckDuckGo result links are inside <article> elements
	var resultURL string
	if err := chromedp.Run(qCtx,
		chromedp.AttributeValue(`article:first-of-type a[href]`, "href", &resultURL, nil),
	); err != nil {
		log.Printf("[BROWSER] ⚠️  Could not locate result link: %v – skipping click", err)
		return nil // not fatal; the search itself counted
	}

	if resultURL != "" {
		log.Printf("[BROWSER]   → Clicking result: %s", truncate(resultURL, 80))
		if err := chromedp.Run(qCtx,
			chromedp.Navigate(resultURL),
			chromedp.WaitReady(`body`, chromedp.ByQuery),
		); err != nil {
			log.Printf("[BROWSER] ⚠️  Could not navigate to result: %v", err)
			return nil // still not fatal
		}

		// Simulate reading: scroll down slowly
		if err := simulateReading(qCtx); err != nil {
			log.Printf("[BROWSER] ⚠️  Scroll error (non-fatal): %v", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Human behaviour helpers
// ---------------------------------------------------------------------------

// typeHumanLike sends each character in text to the focused element with
// a random inter-keystroke delay to avoid bot-detection heuristics.
func typeHumanLike(ctx context.Context, sel, text string) error {
	for _, ch := range text {
		char := string(ch)
		if err := chromedp.Run(ctx,
			chromedp.SendKeys(sel, char, chromedp.ByQuery),
		); err != nil {
			return err
		}
		// 40–120 ms per character — realistic typing speed
		time.Sleep(humanPause(40*time.Millisecond, 120*time.Millisecond))
	}
	return nil
}

// simulateReading performs a series of scroll steps to mimic a human
// scanning a page.
func simulateReading(ctx context.Context) error {
	scrollSteps := 4 + rand.Intn(5) // 4–8 scroll steps
	for i := 0; i < scrollSteps; i++ {
		pixels := 200 + rand.Intn(300) // scroll 200–500px each time
		script := fmt.Sprintf("window.scrollBy({top: %d, behavior: 'smooth'})", pixels)
		if err := chromedp.Run(ctx, chromedp.Evaluate(script, nil)); err != nil {
			return err
		}
		time.Sleep(humanPause(800*time.Millisecond, 2000*time.Millisecond))
	}
	return nil
}

// humanPause returns a random duration between min and max.
func humanPause(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	diff := max - min
	return min + time.Duration(rand.Int63n(int64(diff)))
}

// humanPauseSync blocks for a random duration between min and max.
func humanPauseSync(min, max time.Duration) {
	time.Sleep(humanPause(min, max))
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

// removeHeadlessFlags returns the DefaultExecAllocatorOptions with headless
// flags stripped out, allowing a visible Chrome window during development.
//
// chromedp stores allocator options as opaque functions, so we rebuild from
// the default set by applying each option to a temporary struct and checking
// the resulting args.  The practical portable approach is to use the default
// slice as a base and append a Flag("headless", false) override — Chrome
// honours the last occurrence of a flag.
func removeHeadlessFlags(opts []chromedp.ExecAllocatorOption) []chromedp.ExecAllocatorOption {
	// Append a headless=false override; Chrome uses the last value for
	// duplicate flags, so this effectively disables headless mode without
	// needing to inspect the opaque option functions.
	result := make([]chromedp.ExecAllocatorOption, len(opts))
	copy(result, opts)
	result = append(result,
		chromedp.Flag("headless", false),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
	)
	return result
}

// randomUserAgent returns a realistic desktop user-agent string.
func randomUserAgent() string {
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Safari/605.1.15",
	}
	return agents[rand.Intn(len(agents))]
}

// truncate shortens s to at most n characters, appending "…" if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
