package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

const holdFor = 30 * time.Second

var (
	ErrNotFound = errors.New("not found")
	ErrGone     = errors.New("gone")
)

type Product struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Stock   int    `json:"stock"`
	Initial int    `json:"initial"`
	Image   string `json:"image"`
	Held    int    `json:"held"`
}

type Hold struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Remaining int64  `json:"remaining_ms"`
}

type Catalog struct {
	LiveHolds int       `json:"live_holds"`
	Products  []Product `json:"products"`
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

var seed = []Product{
	{ID: "ash", Name: "Ash bottles", Stock: 1, Image: "img/ash.jpg"},
	{ID: "clay", Name: "Speckled set", Stock: 2, Image: "img/clay.jpg"},
	{ID: "lamp", Name: "Enamel lamp", Stock: 2, Image: "img/lamp.jpg"},
	{ID: "plate", Name: "Blue plate", Stock: 3, Image: "img/plate.jpg"},
	{ID: "amber", Name: "Amber bottle", Stock: 1, Image: "img/amber.jpg"},
	{ID: "cup", Name: "White cup", Stock: 4, Image: "img/cup.jpg"},
	{ID: "pen", Name: "Fountain pen", Stock: 1, Image: "img/pen.jpg"},
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS products (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			stock INTEGER NOT NULL,
			initial INTEGER NOT NULL,
			image TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS holds (
			id TEXT PRIMARY KEY,
			product_id TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		for _, p := range seed {
			_, err := tx.Exec(
				`INSERT INTO products (id, name, stock, initial, image) VALUES (?, ?, ?, ?, ?)`,
				p.ID, p.Name, p.Stock, p.Stock, p.Image,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func sweep(tx *sql.Tx, now time.Time) error {
	ms := now.UnixMilli()
	_, err := tx.Exec(`
		UPDATE products
		SET stock = stock + (
			SELECT COUNT(*) FROM holds
			WHERE holds.product_id = products.id
			  AND holds.status = 'active'
			  AND holds.expires_at <= ?
		)
	`, ms)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`UPDATE holds SET status = 'expired' WHERE status = 'active' AND expires_at <= ?`,
		ms,
	)
	return err
}

func (s *Store) Hold(productID string) (Hold, error) {
	var h Hold
	err := s.withTx(func(tx *sql.Tx) error {
		now := s.clock()
		if err := sweep(tx, now); err != nil {
			return err
		}
		var stock int
		err := tx.QueryRow(`SELECT stock FROM products WHERE id = ?`, productID).Scan(&stock)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE products SET stock = stock - 1 WHERE id = ? AND stock > 0`, productID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrGone
		}
		id, err := newID()
		if err != nil {
			return err
		}
		h = Hold{
			ID:        id,
			ProductID: productID,
			Status:    "active",
			CreatedAt: now.UnixMilli(),
			ExpiresAt: now.Add(holdFor).UnixMilli(),
			Remaining: holdFor.Milliseconds(),
		}
		_, err = tx.Exec(
			`INSERT INTO holds (id, product_id, status, created_at, expires_at) VALUES (?, ?, 'active', ?, ?)`,
			h.ID, h.ProductID, h.CreatedAt, h.ExpiresAt,
		)
		return err
	})
	return h, err
}

func (s *Store) Get(id string) (Hold, error) {
	var h Hold
	err := s.withTx(func(tx *sql.Tx) error {
		now := s.clock()
		if err := sweep(tx, now); err != nil {
			return err
		}
		var err error
		h, err = readHold(tx, id, now)
		return err
	})
	return h, err
}

func (s *Store) Confirm(id string) (Hold, error) {
	return s.finish(id, "confirmed", false)
}

func (s *Store) Cancel(id string) (Hold, error) {
	return s.finish(id, "cancelled", true)
}

func (s *Store) finish(id, status string, restore bool) (Hold, error) {
	var h Hold
	err := s.withTx(func(tx *sql.Tx) error {
		now := s.clock()
		if err := sweep(tx, now); err != nil {
			return err
		}
		var err error
		h, err = readHold(tx, id, now)
		if err != nil {
			return err
		}
		if h.Status != "active" {
			return ErrGone
		}
		if restore {
			if _, err := tx.Exec(`UPDATE products SET stock = stock + 1 WHERE id = ?`, h.ProductID); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`UPDATE holds SET status = ? WHERE id = ? AND status = 'active'`, status, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrGone
		}
		h.Status = status
		h.Remaining = 0
		return nil
	})
	return h, err
}

func (s *Store) Catalog() (Catalog, error) {
	var cat Catalog
	err := s.withTx(func(tx *sql.Tx) error {
		if err := sweep(tx, s.clock()); err != nil {
			return err
		}
		rows, err := tx.Query(`
			SELECT p.id, p.name, p.stock, p.initial, p.image,
			       (SELECT COUNT(*) FROM holds h WHERE h.product_id = p.id AND h.status = 'active')
			FROM products p
			ORDER BY p.rowid
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p Product
			if err := rows.Scan(&p.ID, &p.Name, &p.Stock, &p.Initial, &p.Image, &p.Held); err != nil {
				return err
			}
			cat.Products = append(cat.Products, p)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM holds WHERE status = 'active'`).Scan(&cat.LiveHolds)
	})
	if cat.Products == nil {
		cat.Products = []Product{}
	}
	return cat, err
}

func (s *Store) Reset() (Catalog, error) {
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM holds`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE products SET stock = initial`)
		return err
	})
	if err != nil {
		return Catalog{}, err
	}
	return s.Catalog()
}

func readHold(tx *sql.Tx, id string, now time.Time) (Hold, error) {
	var h Hold
	err := tx.QueryRow(
		`SELECT id, product_id, status, created_at, expires_at FROM holds WHERE id = ?`, id,
	).Scan(&h.ID, &h.ProductID, &h.Status, &h.CreatedAt, &h.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Hold{}, ErrNotFound
	}
	if err != nil {
		return Hold{}, err
	}
	if h.Status == "active" {
		left := h.ExpiresAt - now.UnixMilli()
		if left < 0 {
			left = 0
		}
		h.Remaining = left
	}
	return h, nil
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
