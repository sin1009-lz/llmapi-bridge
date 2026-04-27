package model

import (
	"api-bridge/internal/provider"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ModelStore interface {
	ListModels() ([]*Model, error)
	SaveModels(models []*Model) error
	GetModelRefreshSec() int
}

type ModelLister interface {
	List() ([]*provider.Provider, error)
}

type Fetcher struct {
	store     ModelStore
	registry  ModelLister
	client    *http.Client
	refreshCh chan struct{}
	stopCh    chan struct{}
}

type openaiModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

func NewFetcher(s ModelStore, reg ModelLister) *Fetcher {
	return &Fetcher{
		store:     s,
		registry:  reg,
		client:    &http.Client{Timeout: 30 * time.Second},
		refreshCh: make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
	}
}

func (f *Fetcher) Start() {
	go func() {
		f.Refresh()

		refreshSec := f.store.GetModelRefreshSec()
		if refreshSec <= 0 {
			refreshSec = 3600
		}
		ticker := time.NewTicker(time.Duration(refreshSec) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				f.Refresh()
			case <-f.refreshCh:
				f.Refresh()
			case <-f.stopCh:
				return
			}
		}
	}()
}

func (f *Fetcher) Stop() {
	close(f.stopCh)
}

func (f *Fetcher) TriggerRefresh() {
	select {
	case f.refreshCh <- struct{}{}:
	default:
	}
}

func (f *Fetcher) Refresh() {
	providers, err := f.registry.List()
	if err != nil {
		slog.Error("failed to list providers for model refresh", "error", err)
		return
	}

	var allModels []*Model
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		wg.Add(1)
		go func(prov *provider.Provider) {
			defer wg.Done()
			models, err := f.fetchProviderModels(prov)
			if err != nil {
				slog.Warn("failed to fetch models from provider", "provider", prov.Name, "error", err)
				return
			}
			mu.Lock()
			allModels = append(allModels, models...)
			mu.Unlock()
		}(p)
	}

	wg.Wait()

	if len(allModels) > 0 {
		if err := f.store.SaveModels(allModels); err != nil {
			slog.Error("failed to save models", "error", err)
		} else {
			slog.Info("model refresh completed", "count", len(allModels))
		}
	}
}

func (f *Fetcher) fetchProviderModels(p *provider.Provider) ([]*Model, error) {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if p.DisableV1Prefix {
		baseURL += "/models"
	} else {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Path == "" || parsed.Path == "/" {
			baseURL += "/v1/models"
		} else {
			baseURL += "/models"
		}
	}
	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		return nil, err
	}

	if len(p.APIKeys) > 0 {
		req.Header.Set("Authorization", "Bearer "+p.APIKeys[0])
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp openaiModelsResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	var models []*Model
	now := time.Now()
	for _, d := range apiResp.Data {
		m := &Model{
			ID:           d.ID,
			ProviderID:   p.ID,
			ProviderType: string(p.Type),
			Category:     inferCategory(d.ID),
			OwnedBy:      d.OwnedBy,
			Created:      d.Created,
			UpdatedAt:    now,
		}
		models = append(models, m)
	}

	return models, nil
}

func inferCategory(modelID string) ModelCategory {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "embed"):
		return CategoryEmbedding
	case strings.Contains(id, "image") || strings.Contains(id, "dall-e") || strings.Contains(id, "dalle"):
		return CategoryImage
	case strings.Contains(id, "audio") || strings.Contains(id, "tts") || strings.Contains(id, "whisper"):
		return CategoryAudio
	case strings.Contains(id, "rerank") || strings.Contains(id, "re-rank"):
		return CategoryRerank
	default:
		return CategoryChat
	}
}
