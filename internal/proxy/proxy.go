package proxy

import (
	"api-bridge/internal/account"
	"api-bridge/internal/middleware"
	"api-bridge/internal/model"
	"api-bridge/internal/provider"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Proxy struct {
	registry *provider.Registry
	mapper   *model.Mapper
	client   *http.Client
}

func NewProxy(reg *provider.Registry, mapper *model.Mapper) *Proxy {
	return &Proxy{
		registry: reg,
		mapper:   mapper,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

type ProxyRequest struct {
	Model      string          `json:"model"`
	Stream     bool            `json:"stream,omitempty"`
	RawMessage json.RawMessage `json:"-"`
}

func buildTargetURL(rawBaseURL, endpoint string, disableV1Prefix bool) string {
	baseURL := strings.TrimRight(rawBaseURL, "/")
	if disableV1Prefix {
		return baseURL + endpoint
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return baseURL + "/v1" + endpoint
	}
	return baseURL + endpoint
}

func (p *Proxy) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	p.handleStreamableProxy(w, r, "/chat/completions")
}

func (p *Proxy) HandleCompletions(w http.ResponseWriter, r *http.Request) {
	p.handleStreamableProxy(w, r, "/completions")
}

func (p *Proxy) HandleEmbeddings(w http.ResponseWriter, r *http.Request) {
	p.handleSimpleProxy(w, r, "/embeddings")
}

func (p *Proxy) HandleImageGenerations(w http.ResponseWriter, r *http.Request) {
	p.handleSimpleProxy(w, r, "/images/generations")
}

func (p *Proxy) HandleAudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	p.handleSimpleProxy(w, r, "/audio/transcriptions")
}

func (p *Proxy) HandleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	p.handleSimpleProxy(w, r, "/audio/speech")
}

func (p *Proxy) handleStreamableProxy(w http.ResponseWriter, r *http.Request, endpoint string) {
	acc := r.Context().Value(middleware.AccountKey).(*account.Account)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}

	var proxyReq ProxyRequest
	if err := json.Unmarshal(body, &proxyReq); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	realName, mdl, err := p.mapper.ResolveModel(acc, proxyReq.Model)
	if err != nil {
		http.Error(w, `{"error":"model resolution failed"}`, http.StatusInternalServerError)
		return
	}
	if realName == "" || mdl == nil {
		http.Error(w, `{"error":"model not found or not enabled"}`, http.StatusNotFound)
		return
	}

	targetProvider, err := p.registry.Get(mdl.ProviderID)
	if err != nil || targetProvider == nil {
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	if !targetProvider.Enabled {
		http.Error(w, `{"error":"provider is disabled"}`, http.StatusServiceUnavailable)
		return
	}

	modifiedBody := strings.Replace(string(body), `"`+proxyReq.Model+`"`, `"`+realName+`"`, 1)

	if mdl.Thinking != nil && mdl.Thinking.Mode != model.ThinkingNone {
		payload := mdl.Thinking.ThinkingPayload()
		key := mdl.Thinking.InjectKey()
		if payload != nil && key != "" {
			modifiedBody = injectThinking(modifiedBody, key, payload)
		}
	}

	upstreamReq, err := p.buildUpstreamRequest(r, []byte(modifiedBody), targetProvider, endpoint, realName, proxyReq.Stream)
	if err != nil {
		p.handleProxyError(w, err)
		return
	}

	if proxyReq.Stream {
		p.proxyStreamResponse(w, r, upstreamReq, acc)
	} else {
		p.proxySimpleResponse(w, upstreamReq, acc)
	}
}

func (p *Proxy) handleSimpleProxy(w http.ResponseWriter, r *http.Request, endpoint string) {
	acc := r.Context().Value(middleware.AccountKey).(*account.Account)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}

	var proxyReq ProxyRequest
	if err := json.Unmarshal(body, &proxyReq); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	var realName string
	if proxyReq.Model != "" {
		var mdl *model.Model
		realName, mdl, err = p.mapper.ResolveModel(acc, proxyReq.Model)
		if err != nil {
			http.Error(w, `{"error":"model resolution failed"}`, http.StatusInternalServerError)
			return
		}
		if realName == "" || mdl == nil {
			http.Error(w, `{"error":"model not found or not enabled"}`, http.StatusNotFound)
			return
		}
		body = []byte(strings.Replace(string(body), `"`+proxyReq.Model+`"`, `"`+realName+`"`, 1))
	}

	targetProvider := p.selectProviderForModels(acc)
	if targetProvider == nil {
		http.Error(w, `{"error":"no available provider"}`, http.StatusNotFound)
		return
	}

	upstreamReq, err := p.buildUpstreamRequest(r, body, targetProvider, endpoint, realName, false)
	if err != nil {
		p.handleProxyError(w, err)
		return
	}

	p.proxySimpleResponse(w, upstreamReq, acc)
}

func (p *Proxy) buildUpstreamRequest(r *http.Request, body []byte, targetProvider *provider.Provider, endpoint, realName string, stream bool) (*http.Request, error) {
	var apiKey string
	if targetProvider.Type != provider.ProviderLlamaCpp {
		var ok bool
		apiKey, ok = targetProvider.NextKey()
		if !ok {
			return nil, fmt.Errorf("no API keys available for provider")
		}
	}

	targetURL := buildTargetURL(targetProvider.BaseURL, endpoint, targetProvider.DisableV1Prefix)

	upstreamReq, err := http.NewRequest(r.Method, targetURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request")
	}

	if apiKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	for key := range r.Header {
		if key != "Authorization" {
			upstreamReq.Header.Set(key, r.Header.Get(key))
		}
	}

	slog.Info("proxying request",
		"provider", targetProvider.Name,
		"endpoint", endpoint,
		"targetURL", targetURL,
		"model", realName,
		"stream", stream,
	)

	return upstreamReq, nil
}

func (p *Proxy) handleProxyError(w http.ResponseWriter, err error) {
	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "failed to create upstream request"):
		http.Error(w, `{"error":"failed to create upstream request"}`, http.StatusInternalServerError)
	case strings.Contains(errMsg, "no API keys available for provider"):
		http.Error(w, `{"error":"no API keys available for provider"}`, http.StatusServiceUnavailable)
	default:
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
	}
}

func (p *Proxy) selectProviderForModels(acc *account.Account) *provider.Provider {
	providers, err := p.registry.List()
	if err != nil {
		return nil
	}

	enabledSet := make(map[string]bool)
	for _, id := range acc.EnabledModels {
		enabledSet[id] = true
	}

	for _, prov := range providers {
		if prov.Enabled {
			return prov
		}
	}
	return nil
}

func (p *Proxy) proxyStreamResponse(w http.ResponseWriter, r *http.Request, upstreamReq *http.Request, acc *account.Account) {
	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, `{"error":"upstream request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		if isCORSHeader(k) {
			continue
		}
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(resp.StatusCode)

	reader := NewSSEReader(resp.Body)
	for {
		event, err := reader.ReadEvent()
		if err == io.EOF {
			return
		}
		if err != nil {
			slog.Error("sse read error", "error", err)
			return
		}

		modifiedData := p.replaceModelInJSON(event.Data, acc)
		_, writeErr := fmt.Fprintf(w, "data: %s\n\n", modifiedData)
		if writeErr != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		if strings.HasPrefix(string(event.Data), "[DONE]") {
			return
		}
	}
}

func (p *Proxy) proxySimpleResponse(w http.ResponseWriter, upstreamReq *http.Request, acc *account.Account) {
	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, `{"error":"upstream request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read upstream response"}`, http.StatusBadGateway)
		return
	}

	modifiedBody := p.replaceModelInJSON(body, acc)

	for k, v := range resp.Header {
		if isCORSHeader(k) {
			continue
		}
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(modifiedBody)
}

func (p *Proxy) replaceModelInJSON(data []byte, acc *account.Account) []byte {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return data
	}

	if modelField, ok := rawMap["model"]; ok {
		var modelName string
		if err := json.Unmarshal(modelField, &modelName); err == nil {
			reversed := p.mapper.ReverseMapModel(acc, modelName)
			if reversed != modelName {
				reversedBytes, _ := json.Marshal(reversed)
				rawMap["model"] = reversedBytes
			}
		}
	}

	modified, err := json.Marshal(rawMap)
	if err != nil {
		return data
	}
	return modified
}

func isCORSHeader(header string) bool {
	return strings.HasPrefix(header, "Access-Control-")
}

func injectThinking(body string, key string, payload json.RawMessage) string {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &rawMap); err != nil {
		return body
	}
	rawMap[key] = payload
	modified, err := json.Marshal(rawMap)
	if err != nil {
		return body
	}
	return string(modified)
}
