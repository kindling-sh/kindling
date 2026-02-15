// platform-api is a Go web server that demonstrates kindling with
// multiple backing services. It connects to PostgreSQL, Redis,
// Elasticsearch, Kafka, and Vault using the env vars that the
// DevStagingEnvironment operator auto-injects, and exposes HTTP
// endpoints to verify each connection.
//
// Dependencies (all auto-provisioned):
//
//	postgres        → DATABASE_URL
//	redis           → REDIS_URL
//	elasticsearch   → ELASTICSEARCH_URL
//	kafka           → KAFKA_BROKER_URL
//	vault           → VAULT_ADDR + VAULT_TOKEN
//
// This is intentionally minimal — just enough to prove the full flow:
//
//	git push → GH Actions → self-hosted runner → build → deploy to Kind
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"
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

	log.Printf("platform-api listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// handleRoot returns a friendly hello.
func handleRoot(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{
		"app":     "platform-api",
		"message": "Hello from platform-api on your local Kind cluster! 🚀",
	})
}

// handleHealth is the liveness/readiness probe endpoint.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus checks connectivity to all backing services and reports back.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"app":  "platform-api",
		"time": time.Now().UTC().Format(time.RFC3339),
	}

	status["postgres"] = checkPostgres()
	status["redis"] = checkRedis(r.Context())
	status["elasticsearch"] = checkElasticsearch()
	status["kafka"] = checkKafka()
	status["vault"] = checkVault()

	respond(w, http.StatusOK, status)
}

// ── Postgres ────────────────────────────────────────────────────────

func checkPostgres() map[string]string {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return map[string]string{"status": "not configured (no DATABASE_URL)"}
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	return map[string]string{"status": "connected"}
}

// ── Redis ───────────────────────────────────────────────────────────

func checkRedis(ctx context.Context) map[string]string {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		return map[string]string{"status": "not configured (no REDIS_URL)"}
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	return map[string]string{"status": "connected"}
}

// ── Elasticsearch ───────────────────────────────────────────────────

func checkElasticsearch() map[string]string {
	esURL := os.Getenv("ELASTICSEARCH_URL")
	if esURL == "" {
		return map[string]string{"status": "not configured (no ELASTICSEARCH_URL)"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(esURL)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var info map[string]interface{}
	if err := json.Unmarshal(body, &info); err != nil {
		return map[string]string{"status": "connected", "note": "response not JSON"}
	}

	version := "unknown"
	if v, ok := info["version"].(map[string]interface{}); ok {
		if num, ok := v["number"].(string); ok {
			version = num
		}
	}
	return map[string]string{"status": "connected", "version": version}
}

// ── Kafka ───────────────────────────────────────────────────────────

func checkKafka() map[string]string {
	broker := os.Getenv("KAFKA_BROKER_URL")
	if broker == "" {
		return map[string]string{"status": "not configured (no KAFKA_BROKER_URL)"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := kafka.DialContext(ctx, "tcp", broker)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	defer conn.Close()

	brokers, err := conn.Brokers()
	if err != nil {
		return map[string]string{"status": "connected", "note": "could not list brokers"}
	}
	return map[string]string{
		"status":  "connected",
		"brokers": fmt.Sprintf("%d", len(brokers)),
	}
}

// ── Vault ───────────────────────────────────────────────────────────

func checkVault() map[string]string {
	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		return map[string]string{"status": "not configured (no VAULT_ADDR)"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(vaultAddr + "/v1/sys/health")
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var health map[string]interface{}
	if err := json.Unmarshal(body, &health); err != nil {
		return map[string]string{"status": "reachable", "http_code": fmt.Sprintf("%d", resp.StatusCode)}
	}

	sealed := "unknown"
	if s, ok := health["sealed"].(bool); ok {
		if s {
			sealed = "true"
		} else {
			sealed = "false"
		}
	}
	version := "unknown"
	if v, ok := health["version"].(string); ok {
		version = v
	}
	return map[string]string{
		"status":  "connected",
		"sealed":  sealed,
		"version": version,
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

func respond(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
