package main

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hashicorp/yamux"
	"github.com/stealthpte/server/internal/api"
	"github.com/stealthpte/server/internal/auth"
	"github.com/stealthpte/server/internal/config"
	"github.com/stealthpte/server/internal/db"
	"github.com/stealthpte/server/internal/tunnel"
	"github.com/stealthpte/server/internal/ws"
	"github.com/stealthpte/server/web"
)

func main() {
	cfgPath := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		cfgPath = v
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("mkdir data_dir: %v", err)
	}

	database, err := db.Open(filepath.Join(cfg.DataDir, "db.sqlite"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	jwtAuth, err := auth.New()
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	hub := ws.NewHub(database)

	mgr := tunnel.NewManager(func(clientID string) *yamux.Session {
		return hub.GetYamux(clientID)
	})

	// Wire hub callbacks to tunnel manager
	hub.OnConnect(func(clientID string, _ *yamux.Session) {
		tunnels, err := database.ListTunnels(clientID)
		if err != nil {
			log.Printf("load tunnels for %s: %v", clientID, err)
			return
		}
		mgr.ClientConnected(clientID, tunnels)
	})
	hub.OnDisconnect(func(clientID string) {
		tunnels, err := database.ListTunnels(clientID)
		if err != nil {
			return
		}
		mgr.ClientDisconnected(clientID, tunnels)
	})

	// Wire hub notifications from API
	apiHandler := api.New(database, jwtAuth, cfg, hub)

	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.Handle(cfg.WSPath, hub)

	// Static web UI
	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("embed fs: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/admin/login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "login.html")
	})
	mux.Handle("/admin/", http.StripPrefix("/admin", fileServer))

	// API routes (protected by JWT middleware)
	apiHandler.Register(mux)

	// Protect /admin/* with JWT middleware
	protected := jwtAuth.Middleware(mux)

	// Start existing tunnels on boot
	allTunnels, err := database.ListAllEnabledTunnels()
	if err != nil {
		log.Fatalf("list tunnels: %v", err)
	}
	mgr.StartAll(allTunnels)

	log.Printf("StealthPTE server listening on %s", cfg.Listen)
	log.Printf("WebSocket path: %s", cfg.WSPath)
	log.Printf("Admin UI: http://localhost%s/admin/", cfg.Listen)

	if err := http.ListenAndServe(cfg.Listen, protected); err != nil {
		log.Fatalf("http: %v", err)
	}
}
