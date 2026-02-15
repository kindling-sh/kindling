// inventory is the product-inventory microservice. It stores product
// stock levels in MongoDB and runs a background worker that consumes
// "order.created" events from a Redis queue to automatically decrement
// stock when new orders arrive.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ── Types ───────────────────────────────────────────────────────

type Product struct {
	Name      string    `json:"name"      bson:"name"`
	Stock     int       `json:"stock"     bson:"stock"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

type OrderEvent struct {
	Event   string `json:"event"`
	OrderID int    `json:"order_id"`
	Product string `json:"product"`
	Qty     int    `json:"quantity"`
}

// ── Global clients ──────────────────────────────────────────────

var (
	collection *mongo.Collection
	rdb        *redis.Client
)

const (
	redisQueue = "order_events"
	mongoDB    = "inventory"
	mongoColl  = "products"
)

// ── Main ────────────────────────────────────────────────────────

func main() {
	port := envOr("PORT", "8082")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── MongoDB ─────────────────────────────────────────────────
	mongoURL := os.Getenv("MONGO_URL")
	if mongoURL != "" {
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURL))
		if err != nil {
			log.Fatalf("mongo connect: %v", err)
		}
		if err := client.Ping(ctx, nil); err != nil {
			log.Fatalf("mongo ping: %v", err)
		}
		collection = client.Database(mongoDB).Collection(mongoColl)
		seedProducts(ctx)
		log.Println("✅ MongoDB connected")
	} else {
		log.Println("⚠️  MONGO_URL not set — MongoDB disabled")
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
		log.Println("⚠️  REDIS_URL not set — Redis queue consumer disabled")
	}

	// ── Background queue consumer ───────────────────────────────
	var wg sync.WaitGroup
	if rdb != nil && collection != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumeQueue(ctx)
		}()
		log.Printf("📥 Queue consumer started — watching %q", redisQueue)
	}

	// ── HTTP ────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/inventory", handleListInventory)
	mux.HandleFunc("/status", handleStatus)

	srv := &http.Server{Addr: ":" + port, Handler: mux}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down…")
		cancel()
		srv.Shutdown(context.Background())
	}()

	log.Printf("inventory-service listening on :%s", port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	wg.Wait()
}

// ── Handlers ────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	s := map[string]interface{}{
		"service": "inventory",
		"time":    time.Now().UTC().Format(time.RFC3339),
	}
	if collection != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := collection.Database().Client().Ping(ctx, nil); err != nil {
			s["mongodb"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			count, _ := collection.CountDocuments(ctx, bson.M{})
			s["mongodb"] = map[string]interface{}{"status": "connected", "products": count}
		}
	} else {
		s["mongodb"] = map[string]string{"status": "not configured"}
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

func handleListInventory(w http.ResponseWriter, r *http.Request) {
	if collection == nil {
		http.Error(w, "mongodb not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := collection.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	products := []Product{}
	if err := cur.All(ctx, &products); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respond(w, http.StatusOK, products)
}

// ── Queue consumer ──────────────────────────────────────────────

func consumeQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// BRPOP with a 2-second timeout so we periodically check ctx.Done()
		result, err := rdb.BRPop(ctx, 2*time.Second, redisQueue).Result()
		if err != nil {
			if err == redis.Nil || ctx.Err() != nil {
				continue
			}
			log.Printf("⚠️  queue pop error: %v", err)
			time.Sleep(time.Second)
			continue
		}

		// result[0] = queue name, result[1] = payload
		var evt OrderEvent
		if err := json.Unmarshal([]byte(result[1]), &evt); err != nil {
			log.Printf("⚠️  bad event payload: %v", err)
			continue
		}

		if evt.Event != "order.created" {
			continue
		}

		log.Printf("📦 processing order.created: order #%d — %d × %s",
			evt.OrderID, evt.Qty, evt.Product)

		// Decrement stock in MongoDB (upsert if product doesn't exist)
		filter := bson.M{"name": evt.Product}
		update := bson.M{
			"$inc": bson.M{"stock": -evt.Qty},
			"$set": bson.M{"updated_at": time.Now().UTC()},
		}
		opts := options.Update().SetUpsert(true)
		if _, err := collection.UpdateOne(ctx, filter, update, opts); err != nil {
			log.Printf("⚠️  mongo update failed: %v", err)
		} else {
			log.Printf("✅ stock decremented: %s (-%d)", evt.Product, evt.Qty)
		}
	}
}

// ── Seed data ───────────────────────────────────────────────────

func seedProducts(ctx context.Context) {
	seeds := []Product{
		{Name: "widget-a", Stock: 100, UpdatedAt: time.Now().UTC()},
		{Name: "widget-b", Stock: 250, UpdatedAt: time.Now().UTC()},
		{Name: "gadget-x", Stock: 50, UpdatedAt: time.Now().UTC()},
	}
	for _, p := range seeds {
		filter := bson.M{"name": p.Name}
		update := bson.M{"$setOnInsert": p}
		opts := options.Update().SetUpsert(true)
		collection.UpdateOne(ctx, filter, update, opts)
	}
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
