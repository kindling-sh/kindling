// sample-app is a tiny Go web server that demonstrates the kindling
// developer loop. It connects to PostgreSQL and Redis using the env vars
// that the DevStagingEnvironment operator auto-injects (DATABASE_URL,
// REDIS_URL), and exposes a few HTTP endpoints.
//
// This is intentionally minimal — just enough to prove the full flow:
//   git push → GH Actions → self-hosted runner → build container → deploy to Kind
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/status", handleStatus)

	log.Printf("sample-app listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// handleRoot returns a friendly hello.
func handleRoot(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{
		"app":     "sample-app",
		"message": "Hello from your local Kind cluster! 🚀",
	})
}

// handleHealth is the liveness/readiness probe endpoint.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus checks connectivity to Postgres and Redis and reports back.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"app":  "sample-app",
		"time": time.Now().UTC().Format(time.RFC3339),
	}

	// ── Postgres ─────────────────────────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			status["postgres"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			defer db.Close()
			if err := db.Ping(); err != nil {
				status["postgres"] = map[string]string{"status": "error", "error": err.Error()}
			} else {
				status["postgres"] = map[string]string{"status": "connected"}
			}
		}
	} else {
		status["postgres"] = map[string]string{"status": "not configured (no DATABASE_URL)"}
	}

	// ── Redis ────────────────────────────────────────────────────
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			status["redis"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			rdb := redis.NewClient(opts)
			defer rdb.Close()
			if err := rdb.Ping(r.Context()).Err(); err != nil {
				status["redis"] = map[string]string{"status": "error", "error": err.Error()}
			} else {
				status["redis"] = map[string]string{"status": "connected"}
			}
		}
	} else {
		status["redis"] = map[string]string{"status": "not configured (no REDIS_URL)"}
	}

	respond(w, http.StatusOK, status)
}

func respond(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
