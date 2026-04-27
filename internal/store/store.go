package store

import (
	"api-bridge/internal/account"
	"api-bridge/internal/model"
	"api-bridge/internal/provider"
)

type Settings struct {
	ListenAddr      string                           `json:"listen_addr"`
	ModelRefreshSec int                              `json:"model_refresh_sec"`
	DataDir         string                           `json:"data_dir"`
	ModelThinking   map[string]*model.ThinkingConfig `json:"model_thinking,omitempty"`
}

func (s *Settings) GetModelRefreshSec() int {
	return s.ModelRefreshSec
}

type StoreData struct {
	Accounts  map[string]*account.Account   `json:"accounts"`
	Providers map[string]*provider.Provider `json:"providers"`
	Models    []*model.Model                `json:"models"`
	Settings  *Settings                     `json:"settings"`
}
