// gateway is the public-facing reverse proxy for the microservices demo.
//
// It fans out requests to the orders and inventory backends based on
// URL prefix. Configuration lives in config.go.
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

var conf cfg

func main() {
	conf = loadConfig()

	mux := http.NewServeMux()

	// landing page
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, map[string]string{
			"service": "gateway",
			"version": "0.2.0",
		})
	})

	// readiness probe — k8s hits this
	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, map[string]string{"ready": "true"})
	})

	// ── fan-out routes ──────────────────────────────────────────
	mux.HandleFunc("/orders", proxy(conf.OrdersURL, "/orders"))
	mux.HandleFunc("/orders/", proxy(conf.OrdersURL, ""))
	mux.HandleFunc("/inventory", proxy(conf.InventoryURL, "/inventory"))
	mux.HandleFunc("/inventory/", proxy(conf.InventoryURL, ""))

	// aggregated health across backends
	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		out := map[string]interface{}{
			"service": "gateway",
			"time":    time.Now().UTC().Format(time.RFC3339),
		}
		c := &http.Client{Timeout: 3 * time.Second}
		for name, base := range map[string]string{
			"orders":    conf.OrdersURL,
			"inventory": conf.InventoryURL,
		} {
			resp, err := c.Get(base + "/-/ready")
			if err != nil {
				// fall back to generic health paths
				resp, err = c.Get(base + "/healthcheck")
			}
			if err != nil {
				resp, err = c.Get(base + "/api/v1/health")
			}
			if err != nil {
				out[name] = map[string]string{"status": "unreachable"}
				continue
			}
			resp.Body.Close()
			out[name] = map[string]string{"status": fmt.Sprintf("ok (%d)", resp.StatusCode)}
		}
		respond(w, 200, out)
	}
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/-/status", statusHandler)

	log.Printf("gateway listening on %s", conf.ListenAddr)
	log.Fatal(http.ListenAndServe(conf.ListenAddr, mux))
}

// proxy fans out to a backend service.
func proxy(base, pathOverride string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := base + r.URL.Path
		if pathOverride != "" {
			target = base + pathOverride
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		req.Header = r.Header.Clone()

		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
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

// ── helpers ─────────────────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Printf("WARN: %s not set — backend will be unreachable", key)
		return "http://localhost:0" // placeholder so proxy doesn't panic
	}
	return v
}

func respond(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
