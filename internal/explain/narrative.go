package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Narrator produces short bay prose from a lean Packet.
type Narrator struct {
	Enabled  bool
	APIKey   string
	BaseURL  string // e.g. https://api.openai.com/v1
	Model    string
	HTTP     *http.Client
	cache    sync.Map // fingerprint -> CacheEntry
	CacheTTL time.Duration
}

func NewNarrator(enabled bool, apiKey, baseURL, model string) *Narrator {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &Narrator{
		Enabled:  enabled && apiKey != "",
		APIKey:   apiKey,
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Model:    model,
		HTTP:     &http.Client{Timeout: 45 * time.Second},
		CacheTTL: 24 * time.Hour,
	}
}

type NarrativeResult struct {
	Text        string `json:"text,omitempty"`
	Model       string `json:"model,omitempty"`
	Cached      bool   `json:"cached,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
	SkipReason  string `json:"skip_reason,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Packet      Packet `json:"packet,omitempty"`
}

// Narrate builds/uses the lean packet. force=true when caller passed narrative=1.
func (n *Narrator) Narrate(ctx context.Context, p Packet, force bool) NarrativeResult {
	fp := p.Fingerprint()
	out := NarrativeResult{Fingerprint: fp, Packet: p}

	ok, reason := WorthNarrating(p, force)
	if !ok {
		out.Skipped = true
		out.SkipReason = reason
		return out
	}
	if n == nil || !n.Enabled {
		out.Skipped = true
		out.SkipReason = "llm_disabled"
		return out
	}

	if ent, ok := n.cache.Load(fp); ok {
		ce := ent.(CacheEntry)
		if time.Since(ce.CreatedAt) < n.CacheTTL {
			out.Text = ce.Text
			out.Model = ce.Model
			out.Cached = true
			return out
		}
		n.cache.Delete(fp)
	}

	text, err := n.complete(ctx, p)
	if err != nil {
		out.Skipped = true
		out.SkipReason = "llm_error: " + err.Error()
		return out
	}
	out.Text = text
	out.Model = n.Model
	n.cache.Store(fp, CacheEntry{Text: text, Model: n.Model, CreatedAt: time.Now().UTC()})
	return out
}

func (n *Narrator) complete(ctx context.Context, p Packet) (string, error) {
	body := map[string]any{
		"model": n.Model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt()},
			{"role": "user", "content": UserPrompt(p)},
		},
		"temperature": 0.2,
		// Soft preference for short bay answers — not a context budget.
		"max_tokens": 400,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+n.APIKey)

	resp, err := n.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("empty llm response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
