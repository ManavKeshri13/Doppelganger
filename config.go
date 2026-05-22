// config.go — Project Doppelgänger
//
// Central configuration struct.  Populated once at startup from
// environment variables and threaded through the application via
// dependency injection (no global singletons).

package main

// Config holds every tunable parameter for the application.
type Config struct {
	// LLMApiKey is the Groq API key (env: GROQ_API_KEY).
	// Get a free key at https://console.groq.com/
	LLMApiKey string

	// ServerPort is the TCP port the REST API listens on.
	ServerPort string

	// QueriesPerSession is the number of search queries executed
	// during a single /trigger call.
	QueriesPerSession int

	// BrowserHeadless controls whether chromedp runs in headless mode.
	// Set to false during development to watch the browser work.
	BrowserHeadless bool

	// ChromeUserDataDir is the path to your real Chrome profile directory.
	// When set, chromedp will launch Chrome with that profile so all visited
	// URLs appear in your actual browser history.
	// Example (Windows): C:\Users\YourName\AppData\Local\Google\Chrome\User Data
	// Leave empty to use a fresh temporary profile (default / old behaviour).
	ChromeUserDataDir string
}
