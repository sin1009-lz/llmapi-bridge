package account

import "time"

type Account struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	AccountKey    string            `json:"account_key"`
	EnabledModels []string          `json:"enabled_models"`
	ModelMapping  map[string]string `json:"model_mapping"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}
