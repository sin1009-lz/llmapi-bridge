package store

import (
	"api-bridge/internal/account"
	"api-bridge/internal/model"
	"api-bridge/internal/provider"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type JSONStore struct {
	mu       sync.RWMutex
	data     *StoreData
	dataFile string
}

func NewJSONStore(dataDir string) *JSONStore {
	return &JSONStore{
		data: &StoreData{
			Accounts:  make(map[string]*account.Account),
			Providers: make(map[string]*provider.Provider),
			Models:    []*model.Model{},
			Settings: &Settings{
				ListenAddr:      ":8080",
				ModelRefreshSec: 3600,
				DataDir:         dataDir,
			},
		},
		dataFile: filepath.Join(dataDir, "store.json"),
	}
}

func (s *JSONStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var loaded StoreData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	if loaded.Accounts == nil {
		loaded.Accounts = make(map[string]*account.Account)
	}
	if loaded.Providers == nil {
		loaded.Providers = make(map[string]*provider.Provider)
	}
	if loaded.Models == nil {
		loaded.Models = []*model.Model{}
	}
	if loaded.Settings == nil {
		loaded.Settings = s.data.Settings
	}

	s.data = &loaded
	return nil
}

func (s *JSONStore) Save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(s.dataFile, data, 0644)
}

// --- Accounts ---

func (s *JSONStore) ListAccounts() ([]*account.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	accounts := make([]*account.Account, 0, len(s.data.Accounts))
	for _, acc := range s.data.Accounts {
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

func (s *JSONStore) GetAccount(id string) (*account.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acc, ok := s.data.Accounts[id]
	if !ok {
		return nil, nil
	}
	return acc, nil
}

func (s *JSONStore) GetAccountByKey(key string) (*account.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, acc := range s.data.Accounts {
		if acc.AccountKey == key {
			return acc, nil
		}
	}
	return nil, nil
}

func (s *JSONStore) CreateAccount(acc *account.Account) error {
	s.mu.Lock()
	s.data.Accounts[acc.ID] = acc
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) UpdateAccount(acc *account.Account) error {
	s.mu.Lock()
	s.data.Accounts[acc.ID] = acc
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) DeleteAccount(id string) error {
	s.mu.Lock()
	delete(s.data.Accounts, id)
	s.mu.Unlock()
	return s.saveLocked()
}

// --- Providers ---

func (s *JSONStore) ListProviders() ([]*provider.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	providers := make([]*provider.Provider, 0, len(s.data.Providers))
	for _, p := range s.data.Providers {
		providers = append(providers, p)
	}
	return providers, nil
}

func (s *JSONStore) GetProvider(id string) (*provider.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.data.Providers[id]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (s *JSONStore) CreateProvider(p *provider.Provider) error {
	s.mu.Lock()
	s.data.Providers[p.ID] = p
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) UpdateProvider(p *provider.Provider) error {
	s.mu.Lock()
	s.data.Providers[p.ID] = p
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) DeleteProvider(id string) error {
	s.mu.Lock()
	delete(s.data.Providers, id)
	s.mu.Unlock()
	return s.saveLocked()
}

// --- Models ---

func (s *JSONStore) ListModels() ([]*model.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	models := make([]*model.Model, len(s.data.Models))
	copy(models, s.data.Models)
	return models, nil
}

func (s *JSONStore) SaveModels(models []*model.Model) error {
	s.mu.Lock()
	s.data.Models = models
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) GetModel(id string) (*model.Model, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.data.Models {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, nil
}

// --- Settings ---

func (s *JSONStore) GetModelRefreshSec() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Settings.ModelRefreshSec
}

func (s *JSONStore) GetSettings() *Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.data.Settings
}

func (s *JSONStore) UpdateSettings(settings *Settings) error {
	s.mu.Lock()
	s.data.Settings = settings
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) GetModelThinking() map[string]*model.ThinkingConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.Settings.ModelThinking == nil {
		return make(map[string]*model.ThinkingConfig)
	}
	result := make(map[string]*model.ThinkingConfig)
	for k, v := range s.data.Settings.ModelThinking {
		result[k] = v
	}
	return result
}

func (s *JSONStore) SetModelThinking(modelID string, tc *model.ThinkingConfig) error {
	s.mu.Lock()
	if tc == nil || tc.Mode == model.ThinkingNone {
		if s.data.Settings.ModelThinking != nil {
			delete(s.data.Settings.ModelThinking, modelID)
		}
	} else {
		if s.data.Settings.ModelThinking == nil {
			s.data.Settings.ModelThinking = make(map[string]*model.ThinkingConfig)
		}
		s.data.Settings.ModelThinking[modelID] = tc
	}
	s.mu.Unlock()
	return s.saveLocked()
}

func (s *JSONStore) saveLocked() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(s.dataFile, data, 0644)
}
