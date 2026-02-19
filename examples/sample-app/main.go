// sample-app is a small Go web server that demonstrates the full kindling
// developer loop. It connects to PostgreSQL and Redis (auto-provisioned by
// the operator), serves a dashboard UI, and tracks page visits in Redis.
//
// Just enough to prove the flow end-to-end:
//
//	git push → GH Actions → self-hosted runner → Kaniko build → deploy to Kind
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/lib/pq"
)

var (
	db   *sql.DB
	rdb  *redis.Client
	tmpl *template.Template
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tmpl = template.Must(template.New("page").Parse(pageHTML))

	// ── Connect to backing services (best-effort) ────────────────
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		var err error
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			log.Printf("⚠️  postgres open: %v", err)
		} else {
			db.SetMaxOpenConns(5)
			initSchema()
		}
	}

	if u := os.Getenv("REDIS_URL"); u != "" {
		opts, err := redis.ParseURL(u)
		if err != nil {
			log.Printf("⚠️  redis parse: %v", err)
		} else {
			rdb = redis.NewClient(opts)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/api/status", handleAPIStatus)
	mux.HandleFunc("/api/notes", handleNotes)
	mux.HandleFunc("/healthz", handleHealth)

	log.Printf("sample-app listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// ── Schema ───────────────────────────────────────────────────────

func initSchema() {
	if db == nil {
		return
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id    SERIAL PRIMARY KEY,
		body  TEXT NOT NULL,
		ts    TIMESTAMPTZ DEFAULT NOW()
	)`)
	if err != nil {
		log.Printf("⚠️  schema init: %v", err)
	}
}

// ── Handlers ─────────────────────────────────────────────────────

type pageData struct {
	Hostname string
	Time     string
	Visits   int64
	Postgres string
	Redis    string
	Notes    []note
}

type note struct {
	ID   int
	Body string
	Time string
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := pageData{
		Time: time.Now().UTC().Format("15:04:05 UTC"),
	}
	data.Hostname, _ = os.Hostname()

	// Visit counter (Redis)
	ctx := r.Context()
	if rdb != nil {
		n, err := rdb.Incr(ctx, "sample-app:visits").Result()
		if err == nil {
			data.Visits = n
			data.Redis = "connected"
		} else {
			data.Redis = "error: " + err.Error()
		}
	} else {
		data.Redis = "not configured"
	}

	// Notes list (Postgres)
	if db != nil {
		if err := db.Ping(); err != nil {
			data.Postgres = "error: " + err.Error()
		} else {
			data.Postgres = "connected"
			data.Notes = listNotes()
		}
	} else {
		data.Postgres = "not configured"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, data)
}

func handleNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body := r.FormValue("body")
		if body != "" && db != nil {
			db.Exec("INSERT INTO notes (body) VALUES ($1)", body)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	respondJSON(w, http.StatusOK, listNotes())
}

func listNotes() []note {
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT id, body, ts FROM notes ORDER BY ts DESC LIMIT 20")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var notes []note
	for rows.Next() {
		var n note
		var ts time.Time
		if rows.Scan(&n.ID, &n.Body, &ts) == nil {
			n.Time = ts.Format("Jan 2 15:04")
			notes = append(notes, n)
		}
	}
	return notes
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	s := map[string]interface{}{"app": "sample-app", "time": time.Now().UTC().Format(time.RFC3339)}
	if db != nil {
		if err := db.Ping(); err != nil {
			s["postgres"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			s["postgres"] = map[string]string{"status": "connected"}
		}
	} else {
		s["postgres"] = "not configured"
	}
	if rdb != nil {
		if err := rdb.Ping(ctx).Err(); err != nil {
			s["redis"] = map[string]string{"status": "error", "error": err.Error()}
		} else {
			s["redis"] = map[string]string{"status": "connected"}
		}
	} else {
		s["redis"] = "not configured"
	}
	respondJSON(w, http.StatusOK, s)
}

func respondJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// ── HTML template ────────────────────────────────────────────────

var pageHTML = fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kindling sample-app</title>
<style>
  %s
</style>
</head>
<body>
<div class="shell">
  <header>
    <h1>🔥 kindling <span class="dim">sample-app</span></h1>
    <p class="sub">Running on <code>{{.Hostname}}</code> · {{.Time}}</p>
  </header>

  <section class="cards">
    <div class="card {{if eq .Postgres "connected"}}ok{{else}}warn{{end}}">
      <h2>PostgreSQL</h2>
      <span class="dot"></span>
      <p>{{.Postgres}}</p>
    </div>
    <div class="card {{if eq .Redis "connected"}}ok{{else}}warn{{end}}">
      <h2>Redis</h2>
      <span class="dot"></span>
      <p>{{.Redis}}</p>
      {{if .Visits}}<p class="big">{{.Visits}} <span class="dim">visits</span></p>{{end}}
    </div>
  </section>

  <section class="notes">
    <h2>📝 Notes <span class="dim">(stored in Postgres)</span></h2>
    <form method="POST" action="/api/notes" class="note-form">
      <input type="text" name="body" placeholder="Type something and hit enter…" autocomplete="off" required>
      <button type="submit">Add</button>
    </form>
    {{if .Notes}}
    <ul>
      {{range .Notes}}<li><span class="ts">{{.Time}}</span> {{.Body}}</li>{{end}}
    </ul>
    {{else}}
    <p class="empty">No notes yet — add one above.</p>
    {{end}}
  </section>

  <footer>
    <p>
      <a href="/healthz">/healthz</a> ·
      <a href="/api/status">/api/status</a> ·
      <a href="/api/notes">/api/notes</a>
    </p>
    <p class="dim">Deployed via <strong>kindling</strong> — zero-config local K8s CI/CD</p>
  </footer>
</div>
</body>
</html>`, cssStyles)

var cssStyles = `
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
  background:#0f172a;color:#e2e8f0;min-height:100vh;display:flex;
  justify-content:center;padding:2rem 1rem}
.shell{max-width:640px;width:100%}
header{margin-bottom:2rem}
h1{font-size:1.8rem;font-weight:700;color:#f8fafc}
h1 .dim{color:#64748b;font-weight:400}
.sub{color:#94a3b8;margin-top:.25rem;font-size:.9rem}
code{background:#1e293b;padding:.15em .4em;border-radius:4px;font-size:.85em}
.cards{display:grid;grid-template-columns:1fr 1fr;gap:1rem;margin-bottom:2rem}
.card{background:#1e293b;border-radius:12px;padding:1.25rem;position:relative;
  border:1px solid #334155}
.card h2{font-size:.95rem;font-weight:600;margin-bottom:.5rem;color:#cbd5e1}
.dot{width:10px;height:10px;border-radius:50%;position:absolute;top:1.25rem;
  right:1.25rem}
.card.ok .dot{background:#22c55e;box-shadow:0 0 8px #22c55e80}
.card.warn .dot{background:#f59e0b;box-shadow:0 0 8px #f59e0b80}
.card p{color:#94a3b8;font-size:.85rem}
.big{font-size:2rem;font-weight:700;color:#f8fafc;margin-top:.5rem}
.big .dim{font-size:.9rem;font-weight:400;color:#64748b}
.notes{background:#1e293b;border-radius:12px;padding:1.25rem;
  border:1px solid #334155;margin-bottom:2rem}
.notes h2{font-size:.95rem;font-weight:600;margin-bottom:1rem;color:#cbd5e1}
.note-form{display:flex;gap:.5rem;margin-bottom:1rem}
.note-form input{flex:1;background:#0f172a;border:1px solid #334155;
  border-radius:8px;padding:.5rem .75rem;color:#e2e8f0;font-size:.9rem}
.note-form input:focus{outline:none;border-color:#3b82f6}
.note-form button{background:#3b82f6;color:#fff;border:none;border-radius:8px;
  padding:.5rem 1rem;font-weight:600;cursor:pointer;font-size:.9rem}
.note-form button:hover{background:#2563eb}
ul{list-style:none}
li{padding:.5rem 0;border-top:1px solid #334155;font-size:.9rem;color:#cbd5e1}
.ts{color:#64748b;font-size:.8rem;margin-right:.5rem}
.empty{color:#64748b;font-size:.85rem;font-style:italic}
footer{text-align:center;color:#64748b;font-size:.8rem}
footer a{color:#3b82f6;text-decoration:none}
footer a:hover{text-decoration:underline}
footer strong{color:#94a3b8}
`
