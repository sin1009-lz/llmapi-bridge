package account

import (
	"time"

	"github.com/google/uuid"
)

type AccountStore interface {
	ListAccounts() ([]*Account, error)
	GetAccount(id string) (*Account, error)
	GetAccountByKey(key string) (*Account, error)
	CreateAccount(acc *Account) error
	UpdateAccount(acc *Account) error
	DeleteAccount(id string) error
}

type Manager struct {
	store AccountStore
}

func NewManager(s AccountStore) *Manager {
	return &Manager{store: s}
}

func (m *Manager) Create(name string, enabledModels []string, modelMapping map[string]string) (*Account, error) {
	if modelMapping == nil {
		modelMapping = make(map[string]string)
	}

	acc := &Account{
		ID:            uuid.New().String(),
		Name:          name,
		AccountKey:    GenerateAccountKey(),
		EnabledModels: enabledModels,
		ModelMapping:  modelMapping,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := m.store.CreateAccount(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

func (m *Manager) List() ([]*Account, error) {
	return m.store.ListAccounts()
}

func (m *Manager) Get(id string) (*Account, error) {
	return m.store.GetAccount(id)
}

func (m *Manager) GetByKey(key string) (*Account, error) {
	return m.store.GetAccountByKey(key)
}

func (m *Manager) Update(id string, name string, enabledModels []string, modelMapping map[string]string) (*Account, error) {
	acc, err := m.store.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, nil
	}

	acc.Name = name
	acc.EnabledModels = enabledModels
	acc.ModelMapping = modelMapping
	acc.UpdatedAt = time.Now()

	if err := m.store.UpdateAccount(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

func (m *Manager) Delete(id string) error {
	return m.store.DeleteAccount(id)
}
