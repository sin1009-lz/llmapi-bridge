package router

import (
	"api-bridge/internal/account"
	"api-bridge/internal/middleware"
	"api-bridge/internal/model"
	"api-bridge/internal/provider"
	"api-bridge/internal/proxy"
	"api-bridge/internal/scanner"
	"api-bridge/internal/webui"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type modelLister interface {
	ListModels() ([]*model.Model, error)
}

type thinkingSetter interface {
	SetModelThinking(modelID string, tc *model.ThinkingConfig) error
	GetModelThinking() map[string]*model.ThinkingConfig
}

type Router struct {
	chi.Router
	models         modelLister
	thinkingStore  thinkingSetter
	accountManager *account.Manager
	providerReg    *provider.Registry
	modelFetcher   *model.Fetcher
	modelMapper    *model.Mapper
	proxy          *proxy.Proxy
	scanner        *scanner.Scanner
	adminKey       string
}

func New(
	models modelLister,
	thinkingStore thinkingSetter,
	accountManager *account.Manager,
	providerReg *provider.Registry,
	modelFetcher *model.Fetcher,
	modelMapper *model.Mapper,
	proxyHandler *proxy.Proxy,
	scanner *scanner.Scanner,
	adminKey string,
) *Router {
	r := &Router{
		Router:         chi.NewRouter(),
		models:         models,
		thinkingStore:  thinkingStore,
		accountManager: accountManager,
		providerReg:    providerReg,
		modelFetcher:   modelFetcher,
		modelMapper:    modelMapper,
		proxy:          proxyHandler,
		scanner:        scanner,
		adminKey:       adminKey,
	}

	r.registerRoutes()
	return r
}

func (r *Router) registerRoutes() {
	r.Use(middleware.CORSMiddleware)
	r.Use(middleware.LoggingMiddleware)

	authMW := middleware.AuthMiddleware(r.adminKey, r.accountManager)
	r.Group(func(protected chi.Router) {
		protected.Use(authMW)

		protected.Get("/v1/models", r.handleListModels)

		protected.Post("/v1/chat/completions", r.proxy.HandleChatCompletions)
		protected.Post("/v1/completions", r.proxy.HandleCompletions)
		protected.Post("/v1/embeddings", r.proxy.HandleEmbeddings)
		protected.Post("/v1/images/generations", r.proxy.HandleImageGenerations)
		protected.Post("/v1/audio/transcriptions", r.proxy.HandleAudioTranscriptions)
		protected.Post("/v1/audio/speech", r.proxy.HandleAudioSpeech)

		protected.Route("/admin", func(admin chi.Router) {
			r.registerAdminRoutes(admin)
		})
	})
}

func (r *Router) registerAdminRoutes(admin chi.Router) {
	admin.Post("/accounts", r.handleAdminCreateAccount)
	admin.Get("/accounts", r.handleAdminListAccounts)
	admin.Delete("/accounts/{id}", r.handleAdminDeleteAccount)
	admin.Put("/accounts/{id}/models", r.handleAdminUpdateAccountModels)

	admin.Post("/providers", r.handleAdminCreateProvider)
	admin.Get("/providers", r.handleAdminListProviders)
	admin.Delete("/providers/{id}", r.handleAdminDeleteProvider)
	admin.Put("/providers/{id}", r.handleAdminUpdateProvider)
	admin.Post("/providers/{id}/refresh", r.handleAdminRefreshModels)
	admin.Get("/scanner/llamacpp", r.handleAdminScanLlamaCpp)

	admin.Get("/models", r.handleAdminListModels)
	admin.Get("/models/thinking", r.handleAdminGetModelThinking)
	admin.Put("/models/{id}/thinking", r.handleAdminUpdateModelThinking)
}

func (r *Router) handleListModels(w http.ResponseWriter, req *http.Request) {
	acc := req.Context().Value(middleware.AccountKey).(*account.Account)

	visibleModels, err := r.modelMapper.GetVisibleModels(acc)
	if err != nil {
		http.Error(w, `{"error":"failed to get models"}`, http.StatusInternalServerError)
		return
	}

	type modelData struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}

	data := make([]modelData, 0, len(visibleModels))
	for _, m := range visibleModels {
		data = append(data, modelData{
			ID:      m.ID,
			Object:  "model",
			OwnedBy: m.OwnedBy,
		})
	}

	resp := map[string]interface{}{
		"object": "list",
		"data":   data,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Admin Handlers ---

func (r *Router) handleAdminCreateAccount(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name          string            `json:"name"`
		EnabledModels []string          `json:"enabled_models"`
		ModelMapping  map[string]string `json:"model_mapping"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	acc, err := r.accountManager.Create(body.Name, body.EnabledModels, body.ModelMapping)
	if err != nil {
		http.Error(w, `{"error":"failed to create account"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(acc)
}

func (r *Router) handleAdminListAccounts(w http.ResponseWriter, req *http.Request) {
	accounts, err := r.accountManager.List()
	if err != nil {
		http.Error(w, `{"error":"failed to list accounts"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(accounts)
}

func (r *Router) handleAdminDeleteAccount(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.accountManager.Delete(id); err != nil {
		http.Error(w, `{"error":"failed to delete account"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleAdminUpdateAccountModels(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")

	var body struct {
		EnabledModels []string          `json:"enabled_models"`
		ModelMapping  map[string]string `json:"model_mapping"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	acc, err := r.accountManager.Get(id)
	if err != nil {
		http.Error(w, `{"error":"failed to get account"}`, http.StatusInternalServerError)
		return
	}
	if acc == nil {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}

	updated, err := r.accountManager.Update(id, acc.Name, body.EnabledModels, body.ModelMapping)
	if err != nil {
		http.Error(w, `{"error":"failed to update account"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

func (r *Router) handleAdminCreateProvider(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name            string   `json:"name"`
		Type            string   `json:"type"`
		BaseURL         string   `json:"base_url"`
		APIKeys         []string `json:"api_keys"`
		Enabled         bool     `json:"enabled"`
		DisableV1Prefix bool     `json:"disable_v1_prefix"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	p, err := r.providerReg.Create(body.Name, provider.ProviderType(body.Type), body.BaseURL, body.APIKeys, body.Enabled, body.DisableV1Prefix)
	if err != nil {
		http.Error(w, `{"error":"failed to create provider"}`, http.StatusInternalServerError)
		return
	}

	r.modelFetcher.TriggerRefresh()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (r *Router) handleAdminListProviders(w http.ResponseWriter, req *http.Request) {
	providers, err := r.providerReg.List()
	if err != nil {
		http.Error(w, `{"error":"failed to list providers"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(providers)
}

func (r *Router) handleAdminDeleteProvider(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if err := r.providerReg.Delete(id); err != nil {
		http.Error(w, `{"error":"failed to delete provider"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleAdminUpdateProvider(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")

	var body struct {
		Name            string   `json:"name"`
		Type            string   `json:"type"`
		BaseURL         string   `json:"base_url"`
		APIKeys         []string `json:"api_keys"`
		Enabled         bool     `json:"enabled"`
		DisableV1Prefix bool     `json:"disable_v1_prefix"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	p, err := r.providerReg.Update(id, body.Name, provider.ProviderType(body.Type), body.BaseURL, body.APIKeys, body.Enabled, body.DisableV1Prefix)
	if err != nil {
		http.Error(w, `{"error":"failed to update provider"}`, http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	r.modelFetcher.TriggerRefresh()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (r *Router) handleAdminRefreshModels(w http.ResponseWriter, req *http.Request) {
	r.modelFetcher.TriggerRefresh()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "refresh triggered"})
}

func (r *Router) handleAdminScanLlamaCpp(w http.ResponseWriter, req *http.Request) {
	if r.scanner == nil {
		http.Error(w, `{"error":"scanner not available"}`, http.StatusInternalServerError)
		return
	}

	results := r.scanner.Scan()
	if results == nil {
		results = []scanner.DiscoveredServer{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (r *Router) handleAdminListModels(w http.ResponseWriter, req *http.Request) {
	models, err := r.models.ListModels()
	if err != nil {
		http.Error(w, `{"error":"failed to list models"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

func (r *Router) handleAdminGetModelThinking(w http.ResponseWriter, req *http.Request) {
	models, err := r.models.ListModels()
	if err != nil {
		http.Error(w, `{"error":"failed to list models"}`, http.StatusInternalServerError)
		return
	}

	thinkingConfigs := r.thinkingStore.GetModelThinking()

	type modelThinkingEntry struct {
		ID           string                `json:"id"`
		ProviderID   string                `json:"provider_id"`
		ProviderType string                `json:"provider_type"`
		Category     model.ModelCategory   `json:"category"`
		OwnedBy      string                `json:"owned_by"`
		Thinking     *model.ThinkingConfig `json:"thinking"`
	}

	entries := make([]modelThinkingEntry, 0, len(models))
	for _, m := range models {
		e := modelThinkingEntry{
			ID:           m.ID,
			ProviderID:   m.ProviderID,
			ProviderType: m.ProviderType,
			Category:     m.Category,
			OwnedBy:      m.OwnedBy,
		}
		if tc, ok := thinkingConfigs[m.ID]; ok {
			e.Thinking = tc
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (r *Router) handleAdminUpdateModelThinking(w http.ResponseWriter, req *http.Request) {
	modelID := chi.URLParam(req, "id")
	if modelID == "" {
		http.Error(w, `{"error":"model id required"}`, http.StatusBadRequest)
		return
	}

	modelID, _ = model.TrimThinkingSuffix(modelID)

	var tc model.ThinkingConfig
	if err := json.NewDecoder(req.Body).Decode(&tc); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := r.thinkingStore.SetModelThinking(modelID, &tc); err != nil {
		http.Error(w, `{"error":"failed to save thinking config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (r *Router) WebHandler() http.Handler {
	rt := chi.NewRouter()
	rt.Use(middleware.CORSMiddleware)
	rt.Use(middleware.LoggingMiddleware)
	rt.Get("/", webui.Handler().ServeHTTP)

	authMW := middleware.AuthMiddleware(r.adminKey, r.accountManager)
	rt.Route("/admin", func(admin chi.Router) {
		admin.Use(authMW)
		r.registerAdminRoutes(admin)
	})

	return rt
}
