package storage

import (
	"database/sql"
	"fmt"
	"log"
	"news-bot/internal/news_fetcher"
	"time"

	_ "modernc.org/sqlite"
)

type Storage struct {
	db *sql.DB
}

type Topic struct {
	ID                int64
	Name              string
	DestinationChatID int64
	ReplyToMessageID  int64
}

// ... (Kode dari NewStorage hingga GetNewsSources() sama persis) ...
// ... (Code from NewStorage until GetNewsSources() is identical) ...

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
	// Coba hapus kolom lama jika ada, abaikan error jika gagal (misal, kolom tidak ada)
	s.db.Exec(`ALTER TABLE topics DROP COLUMN message_thread_id;`)

	queries := []string{
		`CREATE TABLE IF NOT EXISTS posted_articles (link TEXT PRIMARY KEY);`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);`,
		`CREATE TABLE IF NOT EXISTS news_sources (id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, url TEXT NOT NULL UNIQUE, link_selector TEXT, topic_id INTEGER, FOREIGN KEY(topic_id) REFERENCES topics(id));`,
		`CREATE TABLE IF NOT EXISTS users (user_id INTEGER PRIMARY KEY, is_admin BOOLEAN NOT NULL DEFAULT FALSE);`,
		`CREATE TABLE IF NOT EXISTS topics (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, destination_chat_id INTEGER DEFAULT 0, reply_to_message_id INTEGER DEFAULT 0);`,
		`CREATE TABLE IF NOT EXISTS pending_articles (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, summary TEXT NOT NULL, link TEXT NOT NULL UNIQUE, image_url TEXT, topic_name TEXT, source_name TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			if e, ok := err.(interface{ ErrorCode() int }); ok && e.ErrorCode() == 1 { // SQLITE_ERROR
				continue
			}
			return err
		}
	}

	// Menambahkan kolom baru ke tabel topics jika belum ada (untuk migrasi)
	alterQueries := []string{
		`ALTER TABLE topics ADD COLUMN destination_chat_id INTEGER DEFAULT 0;`,
		`ALTER TABLE topics ADD COLUMN reply_to_message_id INTEGER DEFAULT 0;`,
	}

	for _, query := range alterQueries {
		// Abaikan error jika kolom sudah ada
		if _, err := s.db.Exec(query); err != nil {
			continue
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
		SELECT s.id, s.type, s.url, s.link_selector, s.topic_id, t.name, t.destination_chat_id, t.reply_to_message_id
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
		var topicID, destChatID, replyToMsgID sql.NullInt64

		if err := rows.Scan(&source.ID, &source.Type, &source.URL, &linkSelector, &topicID, &topicName, &destChatID, &replyToMsgID); err != nil {
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
		if destChatID.Valid {
			source.DestinationChatID = destChatID.Int64
		}
		if replyToMsgID.Valid {
			source.ReplyToMessageID = replyToMsgID.Int64
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
	query := `SELECT id, name, destination_chat_id, reply_to_message_id FROM topics ORDER BY name`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var topic Topic
		var destChatID, replyToMsgID sql.NullInt64
		if err := rows.Scan(&topic.ID, &topic.Name, &destChatID, &replyToMsgID); err != nil {
			return nil, err
		}
		topic.DestinationChatID = destChatID.Int64
		topic.ReplyToMessageID = replyToMsgID.Int64
		topics = append(topics, topic)
	}
	return topics, nil
}

func (s *Storage) DeleteTopic(topicID int64) error {
	query := `DELETE FROM topics WHERE id = ?`
	_, err := s.db.Exec(query, topicID)
	return err
}

func (s *Storage) IsTopicInUse(topicID int64) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM news_sources WHERE topic_id = ?`
	err := s.db.QueryRow(query, topicID).Scan(&count)
	if err != nil {
		return true, err
	}
	return count > 0, nil
}

func (s *Storage) UpdateTopicDestination(topicID int64, chatID int64, messageID int64) error {
	query := `UPDATE topics SET destination_chat_id = ?, reply_to_message_id = ? WHERE id = ?`
	_, err := s.db.Exec(query, chatID, messageID, topicID)
	return err
}

func (s *Storage) GetTopicByName(name string) (*Topic, error) {
	query := `SELECT id, name, destination_chat_id, reply_to_message_id FROM topics WHERE name = ?`
	row := s.db.QueryRow(query, name)

	var topic Topic
	var destChatID, replyToMsgID sql.NullInt64
	if err := row.Scan(&topic.ID, &topic.Name, &destChatID, &replyToMsgID); err != nil {
		return nil, err
	}
	topic.DestinationChatID = destChatID.Int64
	topic.ReplyToMessageID = replyToMsgID.Int64
	return &topic, nil
}

// ... (Sisa file sama persis) ...
// ... (Rest of the file is identical) ...
type PendingArticle struct {
	ID         int64
	Title      string
	Summary    string
	Link       string
	ImageURL   string
	TopicName  string
	SourceName string
	CreatedAt  time.Time
}

func (s *Storage) AddPendingArticle(article PendingArticle) (int64, error) {
	query := `INSERT INTO pending_articles (title, summary, link, image_url, topic_name, source_name) VALUES (?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(query, article.Title, article.Summary, article.Link, article.ImageURL, article.TopicName, article.SourceName)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Storage) GetPendingArticle(id int64) (*PendingArticle, error) {
	query := `SELECT id, title, summary, link, image_url, topic_name, source_name, created_at FROM pending_articles WHERE id = ?`
	row := s.db.QueryRow(query, id)

	var article PendingArticle
	var imageURL, topicName, sourceName sql.NullString
	if err := row.Scan(&article.ID, &article.Title, &article.Summary, &article.Link, &imageURL, &topicName, &sourceName, &article.CreatedAt); err != nil {
		return nil, err
	}

	article.ImageURL = imageURL.String
	article.TopicName = topicName.String
	article.SourceName = sourceName.String

	return &article, nil
}

func (s *Storage) IsArticlePending(link string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM pending_articles WHERE link = ?)`
	err := s.db.QueryRow(query, link).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Storage) DeletePendingArticle(id int64) error {
	query := `DELETE FROM pending_articles WHERE id = ?`
	_, err := s.db.Exec(query, id)
	return err
}

func (s *Storage) UpdatePendingArticleSummary(id int64, summary string) error {
	query := `UPDATE pending_articles SET summary = ? WHERE id = ?`
	_, err := s.db.Exec(query, summary, id)
	return err
}

func (s *Storage) Close() {
	s.db.Close()
}