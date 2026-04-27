package model

import (
	"encoding/json"
	"strings"
	"time"
)

type ModelCategory string

const (
	CategoryChat      ModelCategory = "chat"
	CategoryEmbedding ModelCategory = "embedding"
	CategoryImage     ModelCategory = "image"
	CategoryAudio     ModelCategory = "audio"
	CategoryRerank    ModelCategory = "rerank"
)

type ThinkingMode string

const (
	ThinkingNone          ThinkingMode = ""
	ThinkingToggle        ThinkingMode = "enabled"
	ThinkingReasoningHigh ThinkingMode = "reasoning_high"
	ThinkingReasoningMax  ThinkingMode = "reasoning_max"
)

type ThinkingConfig struct {
	Mode ThinkingMode `json:"mode"`
}

const ThinkingSuffixThink = "-think"
const ThinkingSuffixHigh = "-high"
const ThinkingSuffixMax = "-max"

var thinkingSuffixes = []string{ThinkingSuffixThink, ThinkingSuffixHigh, ThinkingSuffixMax}

func AllThinkingSuffixes() []string { return thinkingSuffixes }

func TrimThinkingSuffix(s string) (string, bool) {
	for _, sfx := range thinkingSuffixes {
		if strings.HasSuffix(s, sfx) {
			return s[:len(s)-len(sfx)], true
		}
	}
	return s, false
}

func (tc *ThinkingConfig) Suffix() string {
	if tc == nil {
		return ""
	}
	switch tc.Mode {
	case ThinkingToggle:
		return ThinkingSuffixThink
	case ThinkingReasoningHigh:
		return ThinkingSuffixHigh
	case ThinkingReasoningMax:
		return ThinkingSuffixMax
	default:
		return ""
	}
}

func (tc *ThinkingConfig) ThinkingPayload() json.RawMessage {
	if tc == nil || tc.Mode == ThinkingNone {
		return nil
	}
	switch tc.Mode {
	case ThinkingToggle:
		return json.RawMessage(`{"type":"enabled"}`)
	case ThinkingReasoningHigh:
		return json.RawMessage(`"high"`)
	case ThinkingReasoningMax:
		return json.RawMessage(`"max"`)
	default:
		return nil
	}
}

func (tc *ThinkingConfig) InjectKey() string {
	if tc == nil || tc.Mode == ThinkingNone {
		return ""
	}
	switch tc.Mode {
	case ThinkingToggle:
		return "thinking"
	case ThinkingReasoningHigh, ThinkingReasoningMax:
		return "reasoning_effort"
	default:
		return ""
	}
}

type Model struct {
	ID            string          `json:"id"`
	ProviderID    string          `json:"provider_id"`
	ProviderType  string          `json:"provider_type"`
	Category      ModelCategory   `json:"category"`
	OwnedBy       string          `json:"owned_by"`
	Created       int64           `json:"created,omitempty"`
	ContextWindow int             `json:"context_window,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
}
