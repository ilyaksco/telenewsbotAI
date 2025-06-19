package bot

const (
	StateAwaitingAIPrompt        = "awaiting_ai_prompt"
	StateAwaitingPostLimit       = "awaiting_post_limit"
	StateAwaitingMessageTemplate = "awaiting_message_template"
	StateAwaitingSchedule        = "awaiting_schedule"
	StateAwaitingSourceURL       = "awaiting_source_url"
	StateAwaitingSourceSelector  = "awaiting_source_selector"
	StateAwaitingTopicName       = "awaiting_topic_name"
	StateAwaitingTopicSelection  = "awaiting_topic_selection"
	StateAwaitingApprovalChatID  = "awaiting_approval_chat_id"
	StateAwaitingArticleEdit     = "awaiting_article_edit"
	StateAwaitingRSSMaxAge       = "awaiting_rss_max_age"
	newsFetchingJobTag           = "news_fetching_job"
)