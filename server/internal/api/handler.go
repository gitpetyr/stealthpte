package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stealthpte/server/internal/auth"
	"github.com/stealthpte/server/internal/config"
	"github.com/stealthpte/server/internal/db"
)

type Handler struct {
	db   *db.DB
	auth *auth.Auth
	cfg  *config.Config
	hub  HubInterface
}

type HubInterface interface {
	NotifyTunnelAdd(clientID string, t *db.Tunnel)
	NotifyTunnelDel(clientID string, tunnelID int64)
}

func New(database *db.DB, a *auth.Auth, cfg *config.Config, hub HubInterface) *Handler {
	return &Handler{db: database, auth: a, cfg: cfg, hub: hub}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/login", h.login)
	mux.HandleFunc("GET /admin/api/v1/config", h.getConfig)
	mux.HandleFunc("GET /admin/api/v1/clients", h.listClients)
	mux.HandleFunc("POST /admin/api/v1/clients", h.createClient)
	mux.HandleFunc("DELETE /admin/api/v1/clients/{id}", h.deleteClient)
	mux.HandleFunc("PUT /admin/api/v1/clients/{id}/token", h.refreshToken)
	mux.HandleFunc("GET /admin/api/v1/clients/{id}/tunnels", h.listTunnels)
	mux.HandleFunc("POST /admin/api/v1/clients/{id}/tunnels", h.createTunnel)
	mux.HandleFunc("PUT /admin/api/v1/clients/{id}/tunnels/{tid}", h.updateTunnel)
	mux.HandleFunc("DELETE /admin/api/v1/clients/{id}/tunnels/{tid}", h.deleteTunnel)
	mux.HandleFunc("GET /admin/api/v1/ports/check", h.checkPort)
}

// GET /admin/api/v1/config
func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"ws_path": h.cfg.WSPath})
}

// POST /admin/login
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Password != h.cfg.AdminPass {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, err := h.auth.IssueToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.auth.SetCookie(w, token)
	writeJSON(w, map[string]string{"token": token})
}

// GET /admin/api/v1/clients
func (h *Handler) listClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.db.ListClients()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type clientResp struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		CreatedAt int64  `json:"created_at"`
		Online    bool   `json:"online"`
	}
	out := make([]clientResp, 0, len(clients))
	for _, c := range clients {
		online := false
		if h.hub != nil {
			online = hubIsOnline(h.hub, c.ID)
		}
		out = append(out, clientResp{
			ID:        c.ID,
			Name:      c.Name,
			Token:     c.Token,
			CreatedAt: c.CreatedAt.Unix(),
			Online:    online,
		})
	}
	writeJSON(w, out)
}

// POST /admin/api/v1/clients
func (h *Handler) createClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	c := &db.Client{
		ID:        randomHex(8),
		Name:      body.Name,
		Token:     randomHex(32),
		CreatedAt: time.Now(),
	}
	if err := h.db.CreateClient(c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		CreatedAt int64  `json:"created_at"`
	}{c.ID, c.Name, c.Token, c.CreatedAt.Unix()})
}

// DELETE /admin/api/v1/clients/{id}
func (h *Handler) deleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.db.DeleteClient(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /admin/api/v1/clients/{id}/token
func (h *Handler) refreshToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := h.db.GetClientByID(id)
	if err != nil || c == nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}
	newToken := randomHex(32)
	if err := h.db.UpdateToken(id, newToken); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	c.Token = newToken
	writeJSON(w, struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		CreatedAt int64  `json:"created_at"`
	}{c.ID, c.Name, c.Token, c.CreatedAt.Unix()})
}

// GET /admin/api/v1/clients/{id}/tunnels
func (h *Handler) listTunnels(w http.ResponseWriter, r *http.Request) {
	tunnels, err := h.db.ListTunnels(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tunnels == nil {
		tunnels = []*db.Tunnel{}
	}
	writeJSON(w, tunnels)
}

// POST /admin/api/v1/clients/{id}/tunnels
func (h *Handler) createTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	var body struct {
		Proto      string `json:"proto"`
		ServerPort int    `json:"server_port"`
		TargetAddr string `json:"target_addr"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.validateTunnel(body.Proto, body.ServerPort, body.TargetAddr); err != "" {
		http.Error(w, err, http.StatusBadRequest)
		return
	}
	used, _ := h.db.IsPortUsed(body.ServerPort)
	if used {
		http.Error(w, "port already in use", http.StatusConflict)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	t := &db.Tunnel{
		ClientID:   clientID,
		Proto:      body.Proto,
		ServerPort: body.ServerPort,
		TargetAddr: body.TargetAddr,
		Enabled:    enabled,
	}
	id, err := h.db.CreateTunnel(t)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t.ID = id
	if enabled && h.hub != nil {
		h.hub.NotifyTunnelAdd(clientID, t)
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

// PUT /admin/api/v1/clients/{id}/tunnels/{tid}
func (h *Handler) updateTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil {
		http.Error(w, "invalid tunnel id", http.StatusBadRequest)
		return
	}
	existing, err := h.db.GetTunnel(tid)
	if err != nil || existing == nil || existing.ClientID != clientID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var body struct {
		Proto      string `json:"proto"`
		ServerPort int    `json:"server_port"`
		TargetAddr string `json:"target_addr"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Proto == "" {
		body.Proto = existing.Proto
	}
	if body.ServerPort == 0 {
		body.ServerPort = existing.ServerPort
	}
	if body.TargetAddr == "" {
		body.TargetAddr = existing.TargetAddr
	}
	if errMsg := h.validateTunnel(body.Proto, body.ServerPort, body.TargetAddr); errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}
	if body.ServerPort != existing.ServerPort {
		used, _ := h.db.IsPortUsed(body.ServerPort)
		if used {
			http.Error(w, "port already in use", http.StatusConflict)
			return
		}
	}
	wasEnabled := existing.Enabled
	enabled := wasEnabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	updated := &db.Tunnel{
		ID:         tid,
		ClientID:   clientID,
		Proto:      body.Proto,
		ServerPort: body.ServerPort,
		TargetAddr: body.TargetAddr,
		Enabled:    enabled,
	}
	if err := h.db.UpdateTunnel(updated); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.hub != nil {
		if wasEnabled {
			h.hub.NotifyTunnelDel(clientID, tid)
		}
		if enabled {
			h.hub.NotifyTunnelAdd(clientID, updated)
		}
	}
	writeJSON(w, updated)
}

// DELETE /admin/api/v1/clients/{id}/tunnels/{tid}
func (h *Handler) deleteTunnel(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("id")
	tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if err != nil {
		http.Error(w, "invalid tunnel id", http.StatusBadRequest)
		return
	}
	if h.hub != nil {
		h.hub.NotifyTunnelDel(clientID, tid)
	}
	if err := h.db.DeleteTunnel(tid, clientID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /admin/api/v1/ports/check?port=X
func (h *Handler) checkPort(w http.ResponseWriter, r *http.Request) {
	portStr := r.URL.Query().Get("port")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	inRange := port >= h.cfg.PortMin && port <= h.cfg.PortMax
	used, _ := h.db.IsPortUsed(port)
	writeJSON(w, map[string]any{
		"port":     port,
		"in_range": inRange,
		"used":     used,
		"ok":       inRange && !used,
	})
}

func (h *Handler) validateTunnel(proto string, port int, target string) string {
	if proto != "tcp" && proto != "udp" {
		return "proto must be tcp or udp"
	}
	if port < h.cfg.PortMin || port > h.cfg.PortMax {
		return "port out of allowed range"
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "target_addr required"
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return "target_addr 格式错误，必须为 host:port（例如 192.168.1.10:80）"
	}
	if strings.TrimSpace(host) == "" {
		return "target_addr host 不能为空"
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "target_addr 端口号必须在 1-65535 之间"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hubIsOnline uses a type assertion to avoid circular imports.
func hubIsOnline(h HubInterface, clientID string) bool {
	type onlineChecker interface {
		IsOnline(id string) bool
	}
	if c, ok := h.(onlineChecker); ok {
		return c.IsOnline(clientID)
	}
	return false
}
