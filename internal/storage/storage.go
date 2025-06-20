package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"news-bot/config"
	"news-bot/internal/news_fetcher"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("storage: record not found")

type Storage struct {
	db *sql.DB
}

type Topic struct {
	ID                int64
	Name              string
	ChatID            int64
	DestinationChatID int64
	ReplyToMessageID  int64
}

type PendingArticle struct {
	ID         int64
	Title      string
	Summary    string
	Link       string
	ImageURL   string
	TopicName  string
	SourceName string
	CreatedAt  time.Time
	ChatID     int64
}

type ConfigWithID struct {
	ChatID int64
	Config *config.Config
	LastFetchedAt time.Time
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
		`CREATE TABLE IF NOT EXISTS chat_configs (
			chat_id INTEGER PRIMARY KEY,
			ai_prompt TEXT NOT NULL,
			gemini_model TEXT NOT NULL,
			message_template TEXT NOT NULL,
			post_limit_per_run INTEGER NOT NULL,
			enable_approval_system BOOLEAN NOT NULL,
			approval_chat_id INTEGER NOT NULL,
			rss_max_age_hours INTEGER NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			language_code TEXT NOT NULL DEFAULT 'id',
			schedule_interval_minutes INTEGER NOT NULL DEFAULT 60,
			last_fetched_at DATETIME
		);`,

		`CREATE TABLE IF NOT EXISTS news_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			link_selector TEXT,
			topic_id INTEGER,
			FOREIGN KEY(topic_id) REFERENCES topics(id) ON DELETE SET NULL,
			UNIQUE(chat_id, url)
		);`,

		`CREATE TABLE IF NOT EXISTS topics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			destination_chat_id INTEGER DEFAULT 0,
			reply_to_message_id INTEGER DEFAULT 0,
			UNIQUE(chat_id, name)
		);`,

		`CREATE TABLE IF NOT EXISTS posted_articles (
			link TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			posted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (link, chat_id)
		);`,

		`CREATE TABLE IF NOT EXISTS pending_articles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			summary TEXT NOT NULL,
			link TEXT NOT NULL,
			image_url TEXT,
			topic_name TEXT,
			source_name TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(chat_id, link)
		);`,

		`CREATE TABLE IF NOT EXISTS users (
			user_id INTEGER PRIMARY KEY,
			is_super_admin BOOLEAN NOT NULL DEFAULT FALSE
		);`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("schema execution failed for query '%s': %w", query, err)
			}
		}
	}

	alterQueries := []string{
		`ALTER TABLE chat_configs ADD COLUMN language_code TEXT NOT NULL DEFAULT 'id'`,
		`ALTER TABLE chat_configs ADD COLUMN schedule_interval_minutes INTEGER NOT NULL DEFAULT 60`,
		`ALTER TABLE chat_configs ADD COLUMN last_fetched_at DATETIME`,
	}
	for _, query := range alterQueries {
		if _, err := s.db.Exec(query); err != nil {
		}
	}

	return nil
}

func (s *Storage) IsChatConfigured(chatID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM chat_configs WHERE chat_id = ?)`
	err := s.db.QueryRow(query, chatID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Storage) CreateDefaultChatConfig(chatID int64, defaultCfg *config.Config) error {
	query := `INSERT OR IGNORE INTO chat_configs (
		chat_id, ai_prompt, gemini_model, message_template,
		post_limit_per_run, enable_approval_system, approval_chat_id,
		rss_max_age_hours, language_code, schedule_interval_minutes
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query,
		chatID,
		defaultCfg.AiPrompt,
		defaultCfg.GeminiModel,
		defaultCfg.TelegramMessageTemplate,
		defaultCfg.PostLimitPerRun,
		defaultCfg.EnableApprovalSystem,
		defaultCfg.ApprovalChatID,
		defaultCfg.RSSMaxAgeHours,
		defaultCfg.LanguageCode,
		defaultCfg.ScheduleIntervalMinutes,
	)
	return err
}

func (s *Storage) GetChatConfig(chatID int64) (*config.Config, error) {
	var cfg config.Config
	query := `SELECT
		ai_prompt, gemini_model, message_template,
		post_limit_per_run, enable_approval_system, approval_chat_id,
		rss_max_age_hours, language_code, schedule_interval_minutes
	FROM chat_configs WHERE chat_id = ?`

	err := s.db.QueryRow(query, chatID).Scan(
		&cfg.AiPrompt,
		&cfg.GeminiModel,
		&cfg.TelegramMessageTemplate,
		&cfg.PostLimitPerRun,
		&cfg.EnableApprovalSystem,
		&cfg.ApprovalChatID,
		&cfg.RSSMaxAgeHours,
		&cfg.LanguageCode,
		&cfg.ScheduleIntervalMinutes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &cfg, nil
}

func (s *Storage) GetAllChatConfigs() ([]*ConfigWithID, error) {
	query := `SELECT
		chat_id, ai_prompt, gemini_model, message_template,
		post_limit_per_run, enable_approval_system, approval_chat_id,
		rss_max_age_hours, language_code, schedule_interval_minutes,
		last_fetched_at
	FROM chat_configs WHERE is_active = TRUE`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*ConfigWithID
	for rows.Next() {
		var chatID int64
		var cfg config.Config
		var lastFetched sql.NullTime

		err := rows.Scan(
			&chatID,
			&cfg.AiPrompt,
			&cfg.GeminiModel,
			&cfg.TelegramMessageTemplate,
			&cfg.PostLimitPerRun,
			&cfg.EnableApprovalSystem,
			&cfg.ApprovalChatID,
			&cfg.RSSMaxAgeHours,
			&cfg.LanguageCode,
			&cfg.ScheduleIntervalMinutes,
			&lastFetched,
		)
		if err != nil {
			return nil, err
		}

		configWithID := &ConfigWithID{ChatID: chatID, Config: &cfg}
		if lastFetched.Valid {
			configWithID.LastFetchedAt = lastFetched.Time
		}

		configs = append(configs, configWithID)
	}
	return configs, nil
}

func (s *Storage) UpdateChatConfig(chatID int64, key string, value interface{}) error {
	query := fmt.Sprintf(`UPDATE chat_configs SET %s = ? WHERE chat_id = ?`, key)
	_, err := s.db.Exec(query, value, chatID)
	return err
}

func (s *Storage) UpdateLastFetchedTime(chatID int64, fetchTime time.Time) error {
	query := `UPDATE chat_configs SET last_fetched_at = ? WHERE chat_id = ?`
	_, err := s.db.Exec(query, fetchTime, chatID)
	return err
}

func (s *Storage) GetLastFetchedTime(chatID int64) (time.Time, error) {
	var lastFetched sql.NullTime
	query := `SELECT last_fetched_at FROM chat_configs WHERE chat_id = ?`
	err := s.db.QueryRow(query, chatID).Scan(&lastFetched)

	if err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}

	if lastFetched.Valid {
		return lastFetched.Time, nil
	}

	return time.Time{}, nil
}

func (s *Storage) MarkAsPosted(link string, chatID int64) error {
	query := `INSERT OR IGNORE INTO posted_articles (link, chat_id) VALUES (?, ?)`
	_, err := s.db.Exec(query, link, chatID)
	return err
}

func (s *Storage) IsAlreadyPosted(link string, chatID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM posted_articles WHERE link = ? AND chat_id = ?)`
	err := s.db.QueryRow(query, link, chatID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Storage) AddNewsSource(chatID int64, source news_fetcher.Source) error {
	query := `INSERT INTO news_sources (chat_id, type, url, link_selector, topic_id) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, chatID, source.Type, source.URL, source.LinkSelector, source.TopicID)
	return err
}

func (s *Storage) GetNewsSourcesForChat(chatID int64) ([]news_fetcher.Source, error) {
	query := `
		SELECT s.id, s.type, s.url, s.link_selector, s.topic_id, t.name, t.destination_chat_id, t.reply_to_message_id
		FROM news_sources s
		LEFT JOIN topics t ON s.topic_id = t.id
		WHERE s.chat_id = ?`
	rows, err := s.db.Query(query, chatID)
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
		source.ChatID = chatID
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

func (s *Storage) GetAllNewsSources() ([]news_fetcher.Source, error) {
	query := `
		SELECT s.id, s.chat_id, s.type, s.url, s.link_selector, s.topic_id, t.name, t.destination_chat_id, t.reply_to_message_id
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

		if err := rows.Scan(&source.ID, &source.ChatID, &source.Type, &source.URL, &linkSelector, &topicID, &topicName, &destChatID, &replyToMsgID); err != nil {
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

func (s *Storage) DeleteNewsSource(id int64, chatID int64) error {
	query := `DELETE FROM news_sources WHERE id = ? AND chat_id = ?`
	_, err := s.db.Exec(query, id, chatID)
	return err
}

func (s *Storage) AddTopic(chatID int64, name string) error {
	query := `INSERT INTO topics (chat_id, name) VALUES (?, ?)`
	_, err := s.db.Exec(query, chatID, name)
	return err
}

func (s *Storage) GetTopicsForChat(chatID int64) ([]Topic, error) {
	query := `SELECT id, name, destination_chat_id, reply_to_message_id FROM topics WHERE chat_id = ? ORDER BY name`
	rows, err := s.db.Query(query, chatID)
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
		topic.ChatID = chatID
		topic.DestinationChatID = destChatID.Int64
		topic.ReplyToMessageID = replyToMsgID.Int64
		topics = append(topics, topic)
	}
	return topics, nil
}

func (s *Storage) DeleteTopic(topicID int64, chatID int64) error {
	query := `DELETE FROM topics WHERE id = ? AND chat_id = ?`
	_, err := s.db.Exec(query, topicID, chatID)
	return err
}

func (s *Storage) IsTopicInUse(topicID int64, chatID int64) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM news_sources WHERE topic_id = ? AND chat_id = ?`
	err := s.db.QueryRow(query, topicID, chatID).Scan(&count)
	if err != nil {
		return true, err
	}
	return count > 0, nil
}

func (s *Storage) UpdateTopicDestination(topicID int64, chatID int64, destChatID int64, messageID int64) error {
	query := `UPDATE topics SET destination_chat_id = ?, reply_to_message_id = ? WHERE id = ? AND chat_id = ?`
	_, err := s.db.Exec(query, destChatID, messageID, topicID, chatID)
	return err
}

func (s *Storage) GetTopicByName(chatID int64, name string) (*Topic, error) {
	query := `SELECT id, name, destination_chat_id, reply_to_message_id FROM topics WHERE chat_id = ? AND name = ?`
	row := s.db.QueryRow(query, chatID, name)

	var topic Topic
	var destChatID, replyToMsgID sql.NullInt64
	if err := row.Scan(&topic.ID, &topic.Name, &destChatID, &replyToMsgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	topic.ChatID = chatID
	topic.DestinationChatID = destChatID.Int64
	topic.ReplyToMessageID = replyToMsgID.Int64
	return &topic, nil
}

func (s *Storage) AddPendingArticle(chatID int64, article PendingArticle) (int64, error) {
	query := `INSERT INTO pending_articles (chat_id, title, summary, link, image_url, topic_name, source_name) VALUES (?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(query, chatID, article.Title, article.Summary, article.Link, article.ImageURL, article.TopicName, article.SourceName)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Storage) GetPendingArticle(id int64) (*PendingArticle, error) {
	query := `SELECT id, chat_id, title, summary, link, image_url, topic_name, source_name, created_at FROM pending_articles WHERE id = ?`
	row := s.db.QueryRow(query, id)

	var article PendingArticle
	var imageURL, topicName, sourceName sql.NullString
	if err := row.Scan(&article.ID, &article.ChatID, &article.Title, &article.Summary, &article.Link, &imageURL, &topicName, &sourceName, &article.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	article.ImageURL = imageURL.String
	article.TopicName = topicName.String
	article.SourceName = sourceName.String
	return &article, nil
}

func (s *Storage) IsArticlePending(link string, chatID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM pending_articles WHERE link = ? AND chat_id = ?)`
	err := s.db.QueryRow(query, link, chatID).Scan(&exists)
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

func (s *Storage) SetSuperAdmin(userID int64, isSuperAdmin bool) error {
	query := `INSERT INTO users (user_id, is_super_admin) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET is_super_admin = excluded.is_super_admin;`
	_, err := s.db.Exec(query, userID, isSuperAdmin)
	return err
}

func (s *Storage) IsSuperAdmin(userID int64) (bool, error) {
	var isSuperAdmin bool
	query := `SELECT is_super_admin FROM users WHERE user_id = ?`
	err := s.db.QueryRow(query, userID).Scan(&isSuperAdmin)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return isSuperAdmin, nil
}