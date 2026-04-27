package model

import "api-bridge/internal/account"

type ModelStoreReader interface {
	ListModels() ([]*Model, error)
	GetModel(id string) (*Model, error)
	GetModelThinking() map[string]*ThinkingConfig
}

type Mapper struct {
	store ModelStoreReader
}

func NewMapper(s ModelStoreReader) *Mapper {
	return &Mapper{store: s}
}

func (m *Mapper) GetVisibleModels(acc *account.Account) ([]*Model, error) {
	allModels, err := m.store.ListModels()
	if err != nil {
		return nil, err
	}

	if len(acc.EnabledModels) == 0 {
		return []*Model{}, nil
	}

	enabledSet := make(map[string]bool)
	for _, id := range acc.EnabledModels {
		enabledSet[id] = true
	}

	thinkingConfigs := m.store.GetModelThinking()

	visible := make([]*Model, 0)
	for _, mdl := range allModels {
		if enabledSet[mdl.ID] {
			if alias, ok := acc.ModelMapping[mdl.ID]; ok {
				mapped := *mdl
				mapped.ID = alias
				visible = append(visible, &mapped)
			} else {
				visible = append(visible, mdl)
			}
			if tc, ok := thinkingConfigs[mdl.ID]; ok && tc.Mode != ThinkingNone {
				sfx := tc.Suffix()
				if sfx == "" {
					continue
				}
				thinkingModel := *mdl
				thinkingModel.ID = thinkingModel.ID + sfx
				thinkingModel.Thinking = tc
				if alias, ok := acc.ModelMapping[mdl.ID]; ok {
					thinkingModel.ID = alias + sfx
				}
				visible = append(visible, &thinkingModel)
			}
		}
	}

	return visible, nil
}

func (m *Mapper) ResolveModel(acc *account.Account, clientModelName string) (string, *Model, error) {
	allModels, err := m.store.ListModels()
	if err != nil {
		return "", nil, err
	}

	thinkingConfigs := m.store.GetModelThinking()

	baseName, hasThinkingSuffix := TrimThinkingSuffix(clientModelName)

	realName := baseName

	for realID, alias := range acc.ModelMapping {
		if alias == baseName {
			realName = realID
			break
		}
	}

	for _, mdl := range allModels {
		if mdl.ID == realName {
			if !isEnabled(acc, realName) {
				return "", nil, nil
			}
			if hasThinkingSuffix {
				if tc, ok := thinkingConfigs[realName]; ok && tc.Mode != ThinkingNone {
					mdlWithThinking := *mdl
					mdlWithThinking.Thinking = tc
					return realName, &mdlWithThinking, nil
				}
				return "", nil, nil
			}
			return realName, mdl, nil
		}
	}

	return "", nil, nil
}

func isEnabled(acc *account.Account, modelID string) bool {
	for _, id := range acc.EnabledModels {
		if id == modelID {
			return true
		}
	}
	return false
}

func (m *Mapper) ReverseMapModel(acc *account.Account, realModelID string) string {
	for realID, alias := range acc.ModelMapping {
		if realID == realModelID {
			return alias
		}
	}
	return realModelID
}
