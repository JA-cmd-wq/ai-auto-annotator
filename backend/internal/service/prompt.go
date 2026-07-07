package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"ai-auto-annotator/backend/internal/config"
	"ai-auto-annotator/backend/internal/model"
)

const PromptPrefix = "Locate all the instances that matches the following description: "

type PromptService struct {
	endpoint string
	model    string
	key      string
	client   *http.Client
}

func NewPromptService(cfg config.Config) *PromptService {
	return &PromptService{
		endpoint: cfg.LLMEndpoint,
		model:    cfg.LLMModel,
		key:      cfg.LLMKey,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func BuildPrompt(desc string) string {
	desc = strings.TrimSpace(desc)
	if strings.HasSuffix(desc, ".") {
		return PromptPrefix + desc
	}
	return PromptPrefix + desc + "."
}

func DefaultPrompt(classes []model.LabelClass) string {
	var parts []string
	for _, c := range classes {
		if strings.TrimSpace(c.PromptFragment) != "" {
			parts = append(parts, strings.TrimSpace(c.PromptFragment))
		}
	}
	return strings.Join(parts, "</c>")
}

func MapLabelToClassID(raw string, classes []model.LabelClass) (*int64, bool) {
	low := strings.ToLower(raw)
	var best *model.LabelClass
	for i := range classes {
		c := &classes[i]
		for _, kw := range c.MatchKeywords {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw != "" && strings.Contains(low, kw) {
				if best == nil || c.ClassIndex < best.ClassIndex {
					best = c
				}
			}
		}
	}
	if best == nil {
		return nil, false
	}
	id := best.ID
	return &id, true
}

func (s *PromptService) Optimize(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	if len([]rune(text)) > 2000 {
		return "", errors.New("text exceeds 2000 characters")
	}
	if s.key == "" {
		return "", errors.New("MATPOOL_KEY is not configured")
	}

	body := map[string]any{
		"model":       s.model,
		"temperature": 0.3,
		"messages": []map[string]string{
			{"role": "system", "content": "Convert the user's object-detection request into concise English category descriptions for an open-vocabulary detector. Output only descriptions separated by </c>. No quotes."},
			{"role": "user", "content": text},
		},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", errors.New(resp.Status)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", errors.New("LLM returned no choices")
	}
	out := strings.Trim(decoded.Choices[0].Message.Content, " \t\r\n\"'")
	if out == "" {
		return "", errors.New("LLM returned empty prompt")
	}
	return out, nil
}
