package provider

import (
	"time"

	"github.com/google/uuid"
)

type ProviderStore interface {
	ListProviders() ([]*Provider, error)
	GetProvider(id string) (*Provider, error)
	CreateProvider(p *Provider) error
	UpdateProvider(p *Provider) error
	DeleteProvider(id string) error
}

type Registry struct {
	store ProviderStore
}

func NewRegistry(s ProviderStore) *Registry {
	return &Registry{store: s}
}

func (r *Registry) Create(name string, ptype ProviderType, baseURL string, apiKeys []string, enabled bool, disableV1Prefix ...bool) (*Provider, error) {
	if apiKeys == nil {
		apiKeys = []string{}
	}

	p := &Provider{
		ID:        uuid.New().String(),
		Name:      name,
		Type:      ptype,
		BaseURL:   baseURL,
		APIKeys:   apiKeys,
		KeyIndex:  0,
		Enabled:   enabled,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if len(disableV1Prefix) > 0 {
		p.DisableV1Prefix = disableV1Prefix[0]
	}

	if err := r.store.CreateProvider(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *Registry) List() ([]*Provider, error) {
	return r.store.ListProviders()
}

func (r *Registry) Get(id string) (*Provider, error) {
	return r.store.GetProvider(id)
}

func (r *Registry) Update(id string, name string, ptype ProviderType, baseURL string, apiKeys []string, enabled bool, disableV1Prefix ...bool) (*Provider, error) {
	p, err := r.store.GetProvider(id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	p.Name = name
	p.Type = ptype
	p.BaseURL = baseURL
	p.APIKeys = apiKeys
	p.Enabled = enabled
	p.UpdatedAt = time.Now()
	if len(disableV1Prefix) > 0 {
		p.DisableV1Prefix = disableV1Prefix[0]
	}

	if err := r.store.UpdateProvider(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (r *Registry) ExistsByBaseURL(baseURL string) bool {
	providers, err := r.store.ListProviders()
	if err != nil {
		return false
	}
	for _, p := range providers {
		if p.BaseURL == baseURL {
			return true
		}
	}
	return false
}

func (r *Registry) Delete(id string) error {
	return r.store.DeleteProvider(id)
}
