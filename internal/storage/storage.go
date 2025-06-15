package storage

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite" // SQLite driver
)

type Storage struct {
	db *sql.DB
}

func NewStorage(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("could not open database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("could not connect to database: %w", err)
	}

	s := &Storage{db: db}
	if err = s.initSchema(); err != nil {
		return nil, fmt.Errorf("could not initialize database schema: %w", err)
	}

	log.Println("Database connection successful and schema initialized.")
	return s, nil
}

func (s *Storage) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS posted_articles (
		link TEXT PRIMARY KEY
	);`
	_, err := s.db.Exec(query)
	return err
}

func (s *Storage) MarkAsPosted(link string) error {
	query := `INSERT OR IGNORE INTO posted_articles (link) VALUES (?)`
	_, err := s.db.Exec(query, link)
	return err
}

func (s *Storage) LoadPostedLinks() (map[string]bool, error) {
	query := `SELECT link FROM posted_articles`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := make(map[string]bool)
	for rows.Next() {
		var link string
		if err := rows.Scan(&link); err != nil {
			return nil, err
		}
		links[link] = true
	}

	log.Printf("Loaded %d previously posted links from database.", len(links))
	return links, nil
}

func (s *Storage) Close() {
	s.db.Close()
}