// orders is the order-management microservice. It stores orders in
// PostgreSQL and publishes "order.created" events to a Redis queue so
// that downstream services (like inventory) can react asynchronously.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
)

// ── Schema ──────────────────────────────────────────────────────

const createTableSQL = `
CREATE TABLE IF NOT EXISTS orders (
    id          SERIAL PRIMARY KEY,
    product     TEXT    NOT NULL,
    quantity    INT     NOT NULL DEFAULT 1,
    status      TEXT    NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

type Order struct {
	ID        int       `json:"id"`
	Product   string    `json:"product"`
	Quantity  int       `json:"quantity"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ── Global clients ──────────────────────────────────────────────

var (
	db  *sql.DB
	rdb *redis.Client
)

const redisQueue = "order_events"

// ── Main ────────────────────────────────────────────────────────

func main() {
	port := envOr("PORT", "8081")
	ctx := context.Background()

	// ── Postgres ────────────────────────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		var err error
		db, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Fatalf("postgres open: %v", err)
		}
		// Auto-migrate
		if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
			log.Fatalf("postgres migrate: %v", err)
		}
		log.Println("✅ Postgres connected")
	} else {
		log.Println("⚠️  DATABASE_URL not set — Postgres disabled")
	}

	// ── Redis ───────────────────────────────────────────────────
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("redis parse: %v", err)
		}
		rdb = redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Fatalf("redis ping: %v", err)
		}
		log.Println("✅ Redis connected")
	} else {
		log.Println("⚠️  REDIS_URL not set — Redis queue disabled")
	}

	// ── Routes ──────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListOrders(w, r)
		case http.MethodPost:
			handleCreateOrder(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/status", handleStatus)

	log.Printf("orders-service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// ── Handlers ────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	s := map[string]interface{}{
		"service": "orders",
		"time":    time.Now().UTC().Format(time.RFC3339),
	}
	if db != nil {
		if err := db.Ping(); err != nil {
			s["postgres"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			s["postgres"] = map[string]string{"status": "connected"}
		}
	} else {
		s["postgres"] = map[string]string{"status": "not configured"}
	}
	if rdb != nil {
		if err := rdb.Ping(r.Context()).Err(); err != nil {
			s["redis"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			qLen := rdb.LLen(r.Context(), redisQueue).Val()
			s["redis"] = map[string]string{
				"status":       "connected",
				"queue":        redisQueue,
				"queue_length": fmt.Sprintf("%d", qLen),
			}
		}
	} else {
		s["redis"] = map[string]string{"status": "not configured"}
	}
	respond(w, http.StatusOK, s)
}

func handleListOrders(w http.ResponseWriter, _ *http.Request) {
	if db == nil {
		http.Error(w, "postgres not configured", http.StatusServiceUnavailable)
		return
	}
	rows, err := db.Query("SELECT id, product, quantity, status, created_at FROM orders ORDER BY id DESC LIMIT 50")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	orders := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Product, &o.Quantity, &o.Status, &o.CreatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		orders = append(orders, o)
	}
	respond(w, http.StatusOK, orders)
}

type createOrderReq struct {
	Product  string `json:"product"`
	Quantity int    `json:"quantity"`
}

func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if db == nil {
		http.Error(w, "postgres not configured", http.StatusServiceUnavailable)
		return
	}

	var req createOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Product == "" {
		http.Error(w, `"product" is required`, http.StatusBadRequest)
		return
	}
	if req.Quantity < 1 {
		req.Quantity = 1
	}

	var order Order
	err := db.QueryRow(
		"INSERT INTO orders (product, quantity) VALUES ($1, $2) RETURNING id, product, quantity, status, created_at",
		req.Product, req.Quantity,
	).Scan(&order.ID, &order.Product, &order.Quantity, &order.Status, &order.CreatedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// ── Publish event to Redis queue ────────────────────────────
	if rdb != nil {
		event, _ := json.Marshal(map[string]interface{}{
			"event":    "order.created",
			"order_id": order.ID,
			"product":  order.Product,
			"quantity": order.Quantity,
			"time":     order.CreatedAt,
		})
		if err := rdb.LPush(r.Context(), redisQueue, event).Err(); err != nil {
			log.Printf("⚠️  failed to publish order event: %v", err)
		} else {
			log.Printf("📤 published order.created event for order #%d to %s", order.ID, redisQueue)
		}
	}

	respond(w, http.StatusCreated, order)
}

// ── Helpers ─────────────────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func respond(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
