package ai

import (
	"context"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Summarizer struct {
	model        *genai.GenerativeModel
	promptFormat string
}

func NewSummarizer(ctx context.Context, apiKey string, promptFormat string) (*Summarizer, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}

	model := client.GenerativeModel("gemini-1.5-flash")
	return &Summarizer{
		model:        model,
		promptFormat: promptFormat,
	}, nil
}

func (s *Summarizer) Summarize(ctx context.Context, articleText string) (string, error) {
	if articleText == "" {
		return "", fmt.Errorf("article text is empty, cannot summarize")
	}

	prompt := fmt.Sprintf("%s \n\n\"%s\"", s.promptFormat, articleText)

	resp, err := s.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("received an empty response from AI")
	}

	summary, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format from AI")
	}

	return string(summary), nil
}