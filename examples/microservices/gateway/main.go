// gateway is the public-facing API for the microservices demo.
// It proxies requests to the orders and inventory services via their
// in-cluster Service DNS names, which are injected as env vars.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := envOr("PORT", "8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/healthz", handleHealth)

	// ── Proxy routes ────────────────────────────────────────────
	mux.HandleFunc("/orders", proxyTo("ORDERS_SERVICE_URL", "/orders"))
	mux.HandleFunc("/orders/", proxyTo("ORDERS_SERVICE_URL", ""))
	mux.HandleFunc("/inventory", proxyTo("INVENTORY_SERVICE_URL", "/inventory"))
	mux.HandleFunc("/inventory/", proxyTo("INVENTORY_SERVICE_URL", ""))

	// ── Aggregated status ───────────────────────────────────────
	mux.HandleFunc("/status", handleStatus)

	log.Printf("gateway listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// ── Handlers ────────────────────────────────────────────────────

func handleRoot(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]string{
		"service": "gateway",
		"message": "Microservices demo — powered by kindling 🔥",
		"routes":  "GET /orders, POST /orders, GET /inventory, GET /status",
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"service": "gateway",
		"time":    time.Now().UTC().Format(time.RFC3339),
	}

	client := &http.Client{Timeout: 3 * time.Second}

	for _, svc := range []struct {
		name   string
		envVar string
	}{
		{"orders", "ORDERS_SERVICE_URL"},
		{"inventory", "INVENTORY_SERVICE_URL"},
	} {
		base := os.Getenv(svc.envVar)
		if base == "" {
			status[svc.name] = map[string]string{"status": "not configured", "env": svc.envVar}
			continue
		}
		resp, err := client.Get(base + "/healthz")
		if err != nil {
			status[svc.name] = map[string]string{"status": "unreachable", "error": err.Error()}
			continue
		}
		resp.Body.Close()
		status[svc.name] = map[string]string{"status": fmt.Sprintf("ok (HTTP %d)", resp.StatusCode)}
	}

	respond(w, http.StatusOK, status)
}

// ── Reverse proxy helper ────────────────────────────────────────

func proxyTo(envVar, pathOverride string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := os.Getenv(envVar)
		if base == "" {
			http.Error(w, fmt.Sprintf("%s not configured", envVar), http.StatusBadGateway)
			return
		}

		target := base + r.URL.Path
		if pathOverride != "" {
			target = base + pathOverride
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		proxyReq.Header = r.Header.Clone()

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
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
