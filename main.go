// main.go — Project Aegis
//
// Entry point.  Loads .env, initialises the global state store,
// and starts the local REST API server.

package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

func main() {
	// ---------------------------------------------------------------
	// 1. Load .env (silently ignored if the file doesn't exist so
	//    real environment variables from the OS still work fine).
	// ---------------------------------------------------------------
	if err := godotenv.Load(); err != nil {
		log.Println("[WARN] No .env file found – falling back to OS environment variables")
	}

	// ---------------------------------------------------------------
	// 2. Validate required configuration
	// ---------------------------------------------------------------
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		log.Fatal("[FATAL] GROQ_API_KEY is not set. Add it to your .env file. Get a free key at https://console.groq.com/")
	}

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8000"
	}

	queriesPerSession := 3
	if v := os.Getenv("QUERIES_PER_SESSION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			queriesPerSession = n
		}
	}

	headless := true
	if v := os.Getenv("BROWSER_HEADLESS"); v == "false" {
		headless = false
	}

	// Default to the standard Windows Chrome profile path;
	// override via CHROME_USER_DATA_DIR in .env if needed.
	chromeUserDataDir := os.Getenv("CHROME_USER_DATA_DIR")
	if chromeUserDataDir == "" {
		chromeUserDataDir = os.Getenv("LOCALAPPDATA") + `\Google\Chrome\User Data`
	}

	// ---------------------------------------------------------------
	// 3. Build the application config
	// ---------------------------------------------------------------
	cfg := &Config{
		LLMApiKey:         apiKey,
		ServerPort:        port,
		QueriesPerSession: queriesPerSession,
		BrowserHeadless:   headless,
		ChromeUserDataDir: chromeUserDataDir,
	}

	// ---------------------------------------------------------------
	// 4. Initialise global state & wire everything together
	// ---------------------------------------------------------------
	state := NewEngineState()
	server := NewServer(cfg, state)

	// ---------------------------------------------------------------
	// 5. Start the API server (blocking)
	// ---------------------------------------------------------------
	addr := fmt.Sprintf(":%s", cfg.ServerPort)
	log.Printf("[INFO] 🛡️  Project Aegis running on http://localhost%s", addr)
	log.Printf("[INFO] LLM provider : Groq / llama-3.3-70b-versatile")
	log.Printf("[INFO] Mode        : Dark Pattern & Privacy Risk Analyser")

	if err := server.Run(addr); err != nil {
		log.Fatalf("[FATAL] Server error: %v", err)
	}
}
