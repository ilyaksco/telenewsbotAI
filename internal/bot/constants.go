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
	newsFetchingJobTag           = "news_fetching_job"
)