package storage

import (
	"database/sql"
	"fmt"
	"log"
	"news-bot/internal/news_fetcher"

	_ "modernc.org/sqlite"
)

type Storage struct {
	db *sql.DB
}

type Topic struct {
	ID   int64
	Name string
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
	queries := []string{
		`CREATE TABLE IF NOT EXISTS posted_articles (link TEXT PRIMARY KEY);`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);`,
		`CREATE TABLE IF NOT EXISTS news_sources (id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, url TEXT NOT NULL UNIQUE, link_selector TEXT, topic_id INTEGER, FOREIGN KEY(topic_id) REFERENCES topics(id));`,
		`CREATE TABLE IF NOT EXISTS users (user_id INTEGER PRIMARY KEY, is_admin BOOLEAN NOT NULL DEFAULT FALSE);`,
		`CREATE TABLE IF NOT EXISTS topics (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE);`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) SetUserAdmin(userID int64, isAdmin bool) error {
	query := `INSERT INTO users (user_id, is_admin) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET is_admin = excluded.is_admin;`
	_, err := s.db.Exec(query, userID, isAdmin)
	return err
}

func (s *Storage) IsUserAdmin(userID int64) (bool, error) {
	var isAdmin bool
	query := `SELECT is_admin FROM users WHERE user_id = ?`
	err := s.db.QueryRow(query, userID).Scan(&isAdmin)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return isAdmin, nil
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

func (s *Storage) GetAllSettings() (map[string]string, error) {
	query := `SELECT key, value FROM settings`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, nil
}

func (s *Storage) SetSetting(key, value string) error {
	query := `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value;`
	_, err := s.db.Exec(query, key, value)
	return err
}

func (s *Storage) AddNewsSource(source news_fetcher.Source) error {
	query := `INSERT INTO news_sources (type, url, link_selector, topic_id) VALUES (?, ?, ?, ?)`
	_, err := s.db.Exec(query, source.Type, source.URL, source.LinkSelector, source.TopicID)
	return err
}

func (s *Storage) GetNewsSources() ([]news_fetcher.Source, error) {
	query := `
		SELECT s.id, s.type, s.url, s.link_selector, s.topic_id, t.name 
		FROM news_sources s 
		LEFT JOIN topics t ON s.topic_id = t.id`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []news_fetcher.Source
	for rows.Next() {
		var source news_fetcher.Source
		var linkSelector, topicName sql.NullString
		var topicID sql.NullInt64
		if err := rows.Scan(&source.ID, &source.Type, &source.URL, &linkSelector, &topicID, &topicName); err != nil {
			return nil, err
		}
		if linkSelector.Valid {
			source.LinkSelector = linkSelector.String
		}
		if topicID.Valid {
			source.TopicID = topicID.Int64
		}
		if topicName.Valid {
			source.TopicName = topicName.String
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (s *Storage) IsNewsSourcesEmpty() (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM news_sources`
	err := s.db.QueryRow(query).Scan(&count)
	if err != nil {
		return true, err
	}
	return count == 0, nil
}

func (s *Storage) DeleteNewsSource(id int64) error {
	query := `DELETE FROM news_sources WHERE id = ?`
	_, err := s.db.Exec(query, id)
	return err
}

func (s *Storage) AddTopic(name string) error {
	query := `INSERT INTO topics (name) VALUES (?)`
	_, err := s.db.Exec(query, name)
	return err
}

func (s *Storage) GetTopics() ([]Topic, error) {
	query := `SELECT id, name FROM topics ORDER BY name`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var topic Topic
		if err := rows.Scan(&topic.ID, &topic.Name); err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return topics, nil
}

func (s *Storage) Close() {
	s.db.Close()
}