package provider

import (
	"sync"
	"time"
)

type ProviderType string

const (
	ProviderOpenAI   ProviderType = "openai"
	ProviderLlamaCpp ProviderType = "llamacpp"
)

type Provider struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Type            ProviderType `json:"type"`
	BaseURL         string       `json:"base_url"`
	APIKeys         []string     `json:"api_keys"`
	KeyIndex        int          `json:"-"`
	Enabled         bool         `json:"enabled"`
	DisableV1Prefix bool         `json:"disable_v1_prefix"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`

	keyMu sync.Mutex
}

func (p *Provider) NextKey() (string, bool) {
	if len(p.APIKeys) == 0 {
		return "", false
	}
	p.keyMu.Lock()
	key := p.APIKeys[p.KeyIndex]
	p.KeyIndex = (p.KeyIndex + 1) % len(p.APIKeys)
	p.keyMu.Unlock()
	return key, true
}
