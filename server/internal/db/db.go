package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

type Client struct {
	ID        string
	Name      string
	Token     string
	CreatedAt time.Time
}

type Tunnel struct {
	ID         int64
	ClientID   string
	Proto      string
	ServerPort int
	TargetAddr string
	Enabled    bool
	RxBytes    int64
	TxBytes    int64
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	d := &DB{sql: sqlDB}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS clients (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    token      TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tunnels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id   TEXT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    proto       TEXT NOT NULL CHECK(proto IN ('tcp','udp')),
    server_port INTEGER NOT NULL UNIQUE,
    target_addr TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    rx_bytes    INTEGER NOT NULL DEFAULT 0,
    tx_bytes    INTEGER NOT NULL DEFAULT 0
);
`)
	return err
}

func (d *DB) Close() error { return d.sql.Close() }

// --- clients ---

func (d *DB) CreateClient(c *Client) error {
	_, err := d.sql.Exec(
		`INSERT INTO clients(id,name,token,created_at) VALUES(?,?,?,?)`,
		c.ID, c.Name, c.Token, c.CreatedAt.Unix(),
	)
	return err
}

func (d *DB) GetClientByToken(token string) (*Client, error) {
	row := d.sql.QueryRow(`SELECT id,name,token,created_at FROM clients WHERE token=?`, token)
	return scanClient(row)
}

func (d *DB) GetClientByID(id string) (*Client, error) {
	row := d.sql.QueryRow(`SELECT id,name,token,created_at FROM clients WHERE id=?`, id)
	return scanClient(row)
}

func (d *DB) ListClients() ([]*Client, error) {
	rows, err := d.sql.Query(`SELECT id,name,token,created_at FROM clients ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) DeleteClient(id string) error {
	_, err := d.sql.Exec(`DELETE FROM clients WHERE id=?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanClient(s scanner) (*Client, error) {
	var c Client
	var ts int64
	if err := s.Scan(&c.ID, &c.Name, &c.Token, &ts); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.CreatedAt = time.Unix(ts, 0)
	return &c, nil
}

// --- tunnels ---

func (d *DB) CreateTunnel(t *Tunnel) (int64, error) {
	res, err := d.sql.Exec(
		`INSERT INTO tunnels(client_id,proto,server_port,target_addr,enabled) VALUES(?,?,?,?,?)`,
		t.ClientID, t.Proto, t.ServerPort, t.TargetAddr, boolToInt(t.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) UpdateTunnel(t *Tunnel) error {
	_, err := d.sql.Exec(
		`UPDATE tunnels SET proto=?,server_port=?,target_addr=?,enabled=? WHERE id=? AND client_id=?`,
		t.Proto, t.ServerPort, t.TargetAddr, boolToInt(t.Enabled), t.ID, t.ClientID,
	)
	return err
}

func (d *DB) DeleteTunnel(id int64, clientID string) error {
	_, err := d.sql.Exec(`DELETE FROM tunnels WHERE id=? AND client_id=?`, id, clientID)
	return err
}

func (d *DB) ListTunnels(clientID string) ([]*Tunnel, error) {
	rows, err := d.sql.Query(
		`SELECT id,client_id,proto,server_port,target_addr,enabled,rx_bytes,tx_bytes FROM tunnels WHERE client_id=? ORDER BY id`,
		clientID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) GetTunnel(id int64) (*Tunnel, error) {
	row := d.sql.QueryRow(
		`SELECT id,client_id,proto,server_port,target_addr,enabled,rx_bytes,tx_bytes FROM tunnels WHERE id=?`, id,
	)
	return scanTunnel(row)
}

func (d *DB) ListAllEnabledTunnels() ([]*Tunnel, error) {
	rows, err := d.sql.Query(
		`SELECT id,client_id,proto,server_port,target_addr,enabled,rx_bytes,tx_bytes FROM tunnels WHERE enabled=1`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) IsPortUsed(port int) (bool, error) {
	var count int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM tunnels WHERE server_port=?`, port).Scan(&count)
	return count > 0, err
}

func (d *DB) AddTraffic(tunnelID int64, rx, tx int64) error {
	_, err := d.sql.Exec(
		`UPDATE tunnels SET rx_bytes=rx_bytes+?,tx_bytes=tx_bytes+? WHERE id=?`,
		rx, tx, tunnelID,
	)
	return err
}

func scanTunnel(s scanner) (*Tunnel, error) {
	var t Tunnel
	var enabled int
	if err := s.Scan(&t.ID, &t.ClientID, &t.Proto, &t.ServerPort, &t.TargetAddr, &enabled, &t.RxBytes, &t.TxBytes); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t.Enabled = enabled != 0
	return &t, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
