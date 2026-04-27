---
name: api-bridge
description: Go AI API gateway - OpenAI-compatible reverse proxy routing to multiple upstream LLM providers. Account-level access control, model alias mapping, thinking mode injection, multi-key round-robin, SSE streaming, embedded Web admin panel, JSON file persistence with RWMutex concurrency.
go_module: api-bridge
go_version: "1.26"
entrypoint: cmd/bridge/main.go
config: config.yaml (YAML)
data: data/store.json (JSON, single-file)
default_ports: {api: 8081, web: 8082}
default_admin_key: sk-admin-default
version_pkg: internal/version (ldflags: -X api-bridge/internal/version.Version=vX.Y -X api-bridge/internal/version.BuildTime=ISO8601)
---

# ARCHITECTURE

```
client -(Bearer account_key)-> middleware/auth.go:AuthMiddleware
  if path prefix /admin/ -> match admin_key, else 401
  else -> account.Manager.GetByKey(token) -> Account{}. put in ctx

router.go:Router.registerRoutes -> chi v5
  global: CORSMiddleware, LoggingMiddleware
  protected group: AuthMiddleware -> /v1/* and /admin/*
    /v1/models             GET   -> handleListModels (reads Account.EnabledModels + ModelMapping + thinking variants)
    /v1/chat/completions   POST  -> proxy.HandleChatCompletions
    /v1/completions        POST  -> proxy.HandleCompletions
    /v1/embeddings         POST  -> proxy.HandleEmbeddings
    /v1/images/generations POST  -> proxy.HandleImageGenerations
    /v1/audio/transcriptions POST -> proxy.HandleAudioTranscriptions
    /v1/audio/speech       POST  -> proxy.HandleAudioSpeech
    /admin/accounts        GET|POST        -> account CRUD
    /admin/accounts/{id}   DELETE          -> account CRUD
    /admin/accounts/{id}/models PUT        -> update EnabledModels + ModelMapping
    /admin/providers       GET|POST        -> provider CRUD
    /admin/providers/{id}  PUT|DELETE      -> provider CRUD
    /admin/providers/{id}/refresh POST     -> model.Fetcher.TriggerRefresh
    /admin/models          GET             -> store.ListModels
    /admin/models/thinking GET             -> store.GetModelThinking
    /admin/models/{id}/thinking PUT        -> store.SetModelThinking
    /admin/scanner/llamacpp GET            -> scanner.Scan (lan discovery)
  webHandler (WebHandler): separate chi router on web_listen_addr
    / -> webui.Handler() serve embedded index.html
    /admin/* -> same admin routes with auth

Web UI nav tabs: dashboard, providers, accounts, models, thinking
Admin key hardcoded in frontend JS: const ADMIN_KEY = 'Bearer sk-admin-default'
Web UI calls /admin/* endpoints via fetch() with ADMIN_KEY header.
```

# DATA_MODEL

```yaml
# data/store.json structure
accounts: map[uuid]Account
  Account:
    id: string(uuid)
    name: string
    account_key: "sk-bridge-<32hex>"  # crypto/rand 16bytes hex
    enabled_models: []string           # real model IDs this account can access
    model_mapping: map[string]string   # realID -> alias. Reverse lookup on request
    created_at: time
    updated_at: time

providers: map[uuid]Provider
  Provider:
    id: string(uuid)
    name: string
    type: "openai"|"llamacpp"
    base_url: string                  # upstream API root
    api_keys: []string                # multiple keys for round-robin
    keyMu: sync.Mutex                 # NOT serialized, protects KeyIndex/NextKey
    KeyIndex: int                     # current round-robin position
    enabled: bool
    disable_v1_prefix: bool           # if true, skip /v1 in URL construction
    created_at: time
    updated_at: time

models: []Model                       # flat list, populated by fetcher
  Model:
    id: string                        # upstream model name
    provider_id: string(uuid)         # FK -> Provider
    provider_type: string             # denormalized for quick filter
    category: "chat"|"embedding"|"image"|"audio"|"rerank"
    owned_by: string
    created: int64
    context_window: int
    thinking: ThinkingConfig|null
    updated_at: time

settings:
  model_refresh_sec: int              # default 3600
  model_thinking: map[modelID]ThinkingConfig
    ThinkingConfig:
      mode: ""|"enabled"|"reasoning_high"|"reasoning_max"
      # "" = no thinking
      # "enabled" -> inject thinking:{type:"enabled"}, suffix -think
      # "reasoning_high" -> inject reasoning_effort:"high", suffix -high
      # "reasoning_max" -> inject reasoning_effort:"max", suffix -max
```

# CONFIG

```yaml
# config.yaml
server:
  listen_addr: ":8081"
  web_listen_addr: ":8082"
  admin_key: "sk-admin-default"
storage:
  data_dir: "./data"
  model_refresh_sec: 3600
providers:                           # auto-imported on first run if not in store.json
  - name: string
    type: "openai"|"llamacpp"
    base_url: string
    api_keys: [string]
    enabled: bool
```

Config loading: `config.LoadConfig(path)` -> merges onto DefaultConfig().
Providers in config.yaml only created once (dedup by name against store.json).

# REQUEST_FLOW

```
POST /v1/chat/completions {"model":"alias-name","stream":bool,"messages":[...]}

1. AuthMiddleware -> extract token -> GetAccountByKey(key) O(n) linear scan accounts map
   -> ctx.WithValue(AccountKey, acc)

2. proxy.HandleChatCompletions(w,r) -> handleStreamableProxy(w,r,"/chat/completions")
  2a. io.ReadAll body -> json.Unmarshal into ProxyRequest {Model, Stream}
  2b. mapper.ResolveModel(acc, proxyReq.Model):
      - TrimThinkingSuffix(name) -> (baseName, hasSuffix:bool)
      - reverse lookup acc.ModelMapping: if alias==baseName, realName=realID
      - linear scan allModels for mdl.ID==realName
      - check acc.EnabledModels contains realName (isEnabled)
      - if hasSuffix, fetch GetModelThinking[realName], attach to returned Model
      - returns (realName, *Model, error)
  2c. registry.Get(mdl.ProviderID) -> *Provider
  2d. check Provider.Enabled
  2e. strings.Replace body "model":"alias" -> "model":"realName" (first occurrence only)
  2f. if mdl.Thinking != nil && mode != ThinkingNone:
        injectThinking(body, key=InjectKey(), payload=ThinkingPayload())
        - "enabled" -> inject "thinking": {"type":"enabled"}
        - "reasoning_high"/"reasoning_max" -> inject "reasoning_effort":"high"/"max"
  2g. if Provider.Type != llamacpp: apiKey = Provider.NextKey() (Mutex-protected round-robin)
  2h. buildTargetURL(baseURL, endpoint, DisableV1Prefix):
        if DisableV1Prefix: baseURL + endpoint
        else if parsed baseURL has no path: baseURL + "/v1" + endpoint
        else: baseURL + endpoint
  2i. http.NewRequest to upstream, copy headers (skip Authorization), set Bearer apiKey
  2j. if stream -> proxyStreamResponse; else -> proxySimpleResponse

3. proxyStreamResponse:
  - http.Client.Do(upstreamReq)
  - SSE loop: NewSSEReader(resp.Body).ReadEvent()
    each event: replaceModelInJSON(event.Data, acc) using mapper.ReverseMapModel
    fmt.Fprintf(w, "data: %s\n\n", modified)
    http.Flusher.Flush()
  - stop on [DONE] prefix

4. proxySimpleResponse:
  - http.Client.Do(upstreamReq)
  - io.ReadAll body
  - replaceModelInJSON(body, acc)
  - copy headers (skip CORS), write status, write modified body

5. replaceModelInJSON:
  - json.Unmarshal into map[string]json.RawMessage
  - if "model" key exists, ReverseMapModel(acc, modelName)
  - json.Marshal back
```

# CONCURRENCY_MODEL

```
json_store.go: JSONStore
  mu: sync.RWMutex
  READ path: RLock -> copy data -> RUnlock  (ListModels, GetAccount, ListProviders, etc.)
  WRITE path (all 9 methods refactored v1.0):
    Lock -> mutate s.data.* -> Unlock -> saveLocked()
    saveLocked(): RLock -> json.MarshalIndent(s.data) -> RUnlock -> os.MkdirAll -> os.WriteFile
    I/O outside any lock; serialization under RLock (concurrent with reads)

provider.go: Provider.NextKey()
  keyMu: sync.Mutex
  Lock -> read APIKeys[KeyIndex], KeyIndex = (KeyIndex+1) % len -> Unlock
  Thread-safe round-robin across arbitrary concurrency

fetcher.go: Fetcher.Refresh()
  concurrent goroutines per provider (one per enabled Provider)
  mu sync.Mutex for appending to allModels slice
  wg.Wait() then SaveModels (lock -> assign -> unlock -> saveLocked I/O)

scanner.go: Scanner.Scan()
  semaphore chan struct{}{} cap=50 for concurrency limiting
  scans all /24 subnets on non-loopback interfaces
  probes ports 8080/8081/8085 with paths /v1/models, /models, /api/models
  timeout 1.5s per probe
```

# STORE_INVARIANTS

- After Load(), all nil maps/slices are initialized to empty (defensive)
- Models populated exclusively by Fetcher (from provider /v1/models endpoints)
- Account.account_key generated by crypto/rand hex (16 bytes -> 32 hex chars)
- Provider.KeyIndex NOT persisted (json:"-"), always starts at 0 on load
- Provider.keyMu NOT persisted, always fresh zero-value on load
- Thinking suffix variants (-think/-high/-max) NOT stored as separate Model records; generated dynamically in GetVisibleModels

# BUILD_DEPLOY

```bash
# Windows
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -ldflags="-s -w -X api-bridge/internal/version.Version=v1.0 -X api-bridge/internal/version.BuildTime=$(Get-Date -Format 'yyyy-MM-dd')" -o release/api-bridge-windows-amd64.exe ./cmd/bridge/

# Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X api-bridge/internal/version.Version=v1.0 -X api-bridge/internal/version.BuildTime=$(date -I)" -o release/api-bridge-linux-amd64 ./cmd/bridge/

# flags: -s strip debug info, -w strip DWARF, -X inject version vars
# without -X: version=dev, buildTime=unknown
```

Deploy: copy binary + config.yaml + data/ directory to target.
First run creates data/store.json if missing (Load handles ENOENT).
Config providers auto-imported on first run (dedup by name).

# API_QUICKREF

```yaml
Public API (Auth: Bearer <account_key>):
  GET    /v1/models                  -> {object:"list", data:[{id,object,owned_by}]}
  POST   /v1/chat/completions        -> stream:true gives SSE, stream:false gives JSON
  POST   /v1/completions             -> text completion
  POST   /v1/embeddings              -> vector embedding
  POST   /v1/images/generations      -> image generation
  POST   /v1/audio/transcriptions    -> speech-to-text
  POST   /v1/audio/speech            -> text-to-speech

Admin API (Auth: Bearer <admin_key>):
  GET    /admin/accounts             -> []Account
  POST   /admin/accounts             body:{name,enabled_models,model_mapping} -> Account
  DELETE /admin/accounts/{id}        -> 204
  PUT    /admin/accounts/{id}/models body:{enabled_models,model_mapping} -> Account
  GET    /admin/providers            -> []Provider
  POST   /admin/providers            body:{name,type,base_url,api_keys,enabled,disable_v1_prefix?} -> Provider
  PUT    /admin/providers/{id}       same body -> Provider
  DELETE /admin/providers/{id}       -> 204
  POST   /admin/providers/{id}/refresh -> {status:"refresh triggered"}
  GET    /admin/models               -> []Model
  GET    /admin/models/thinking      -> [{id,provider_id,provider_type,category,owned_by,thinking}]
  PUT    /admin/models/{id}/thinking body:{mode:""|"enabled"|"reasoning_high"|"reasoning_max"} -> {status:"ok"}
  GET    /admin/scanner/llamacpp     -> []DiscoveredServer
  GET    /                            -> Web UI (embedded index.html)
```

# DEBUG

```yaml
slog_format: text (slog.NewTextHandler, stdout, LevelInfo)
log_patterns:
  startup: 'level=INFO msg="api-bridge starting" version=<v> build=<date>'
  server: 'level=INFO msg="starting API server" addr=<addr>'
  proxy: 'level=INFO msg="proxying request" provider=<name> endpoint=<path> targetURL=<url> model=<realName> stream=<bool>'
  request: 'level=INFO msg=request method=<m> path=<p> remote_addr=<ip> status=<code> duration=<d>'
  auth_fail: 'level=WARN msg="auth failed: invalid account key" method=... path=... remote_addr=...'
  model_refresh: 'level=INFO msg="model refresh completed" count=<n>'
  refresh_fail: 'level=WARN msg="failed to fetch models from provider" provider=<name> error=<err>'

common_issues:
  - "invalid account key": token not in store.json accounts, or malformed Authorization header
  - "model not found or not enabled": model name not in Account.EnabledModels
  - "provider not found": model's provider_id references deleted provider
  - "no API keys available": Provider.api_keys is empty and type is not llamacpp
  - "upstream request failed": http.Client.Do error (network, timeout, TLS)

check_store: cat data/store.json | python -m json.tool
check_health: curl http://localhost:8081/v1/models -H "Authorization: Bearer <key>"
check_admin: curl http://localhost:8081/admin/accounts -H "Authorization: Bearer <admin_key>"
force_refresh: curl -X POST http://localhost:8081/admin/providers/<uuid>/refresh -H "Authorization: Bearer <admin_key>"

modify_store_live: edit data/store.json directly while service running.
  Service reads from memory (loaded at startup, persisted on write ops).
  Manual edits require service restart OR trigger admin API write to force reload.
```

# MODIFICATION_RULES

```yaml
DO_NOT_REMOVE:
  - provider.go: keyMu sync.Mutex in Provider struct, Lock/Unlock in NextKey()
  - json_store.go: Lock->mutate->Unlock->saveLocked pattern in CreateAccount/UpdateAccount/DeleteAccount/CreateProvider/UpdateProvider/DeleteProvider/SaveModels/UpdateSettings/SetModelThinking
  - json_store.go: saveLocked() uses RLock for serialization (concurrent with reads), I/O outside lock
  - mapper.go: ResolveModel(request) and ReverseMapModel(response) must be called in pairs per request
  - proxy.go: replaceModelInJSON called on EVERY response (stream + simple) to reverse-model-map
  - proxy.go: proxyStreamResponse uses http.Flusher per event; do NOT buffer the full stream

adding_new_endpoint:
  1. Add handler to proxy.go (follow handleStreamableProxy or handleSimpleProxy pattern)
  2. Register route in router.go registerRoutes() under protected group
  3. If endpoint has "model" field -> call mapper.ResolveModel (request) and replaceModelInJSON (response)
  4. If endpoint has no model field -> use selectProviderForModels to pick a default provider

data_model_change:
  - store.json is the persistence layer; update StoreData in store.go
  - update Load() nil-initialization for new fields
  - update all write methods if field is mutable

webui_change:
  - index.html in internal/webui/ is embedded via //go:embed
  - admin KEY is hardcoded as 'Bearer sk-admin-default' in frontend JS (line ~825)
  - nav tabs: dashboard, providers, accounts, models, thinking (data-page attributes)
```

# TEST_MATRIX

```yaml
smoke:
  - {name: service_up, method: GET, url: /v1/models, auth: account_key, expect: 200}
  - {name: auth_reject, method: GET, url: /v1/models, auth: invalid_key, expect: 401}
  - {name: admin_access, method: GET, url: /admin/accounts, auth: admin_key, expect: 200}
  - {name: admin_reject, method: GET, url: /admin/accounts, auth: account_key, expect: 401}

functional:
  - {name: chat_nonstream, method: POST, url: /v1/chat/completions, body: {model, messages, stream:false}, expect: 200, model returns alias}
  - {name: chat_stream, method: POST, url: /v1/chat/completions, body: {model, messages, stream:true}, expect: SSE stream, model returns alias}
  - {name: thinking_variant, setup: PUT /admin/models/{id}/thinking {mode:reasoning_max}, method: GET, url: /v1/models, expect: list contains {id}-max}
  - {name: model_alias, setup: PUT /admin/accounts/{id}/models {model_mapping:{real:"alias"}}, method: POST, url: /v1/chat/completions, body: {model:"alias"}, expect: response model="alias"}

concurrency:
  - {name: nonstream_200, iterations: 200, parallel: true, expect: 100% success}
  - {name: stream_200, iterations: 200, parallel: true, stream: true, expect: 100% success, no model mismatch}
  - {name: refresh_nonblocking, setup: POST /admin/providers/{id}/refresh simultaneously with 30x POST /v1/chat/completions, expect: all 30 succeed}

latency:
  - {name: bridge_vs_direct, method: POST stream:true to bridge AND direct upstream simultaneously per round, 20+ rounds, expect: |median_diff| < upstream jitter (seconds)}
```

# FILE_MAP

```yaml
cmd/bridge/main.go:
  - main(): init slog, load config, create JSONStore.Load, init managers/registry/fetcher/mapper/proxy/scanner
  - start two http.Server goroutines (api on listen_addr, web on web_listen_addr)
  - signal handler for graceful shutdown (save store, stop fetcher)
  - initProvidersFromConfig(): one-time import from config.yaml providers section

internal/account/account.go: Account struct definition
internal/account/keygen.go: GenerateAccountKey() -> "sk-bridge-" + hex(rand16)
internal/account/manager.go: Manager{AccountStore}. Create/List/Get/GetByKey/Update/Delete

internal/config/config.go: Config{Server,Storage,Providers}. LoadConfig(path). DefaultConfig()

internal/middleware/auth.go: AuthMiddleware(adminKey, accountManager). extractToken from Authorization/X-API-Key. admin paths -> match adminKey. other -> GetAccountByKey. puts Account in ctx
internal/middleware/cors.go: CORSMiddleware. permissive CORS (mirrors Origin, allows all methods)
internal/middleware/logging.go: LoggingMiddleware. logs method/path/remote_addr/status/duration

internal/model/model.go: Model, ModelCategory, ThinkingConfig, ThinkingMode constants, TrimThinkingSuffix, Suffix, ThinkingPayload, InjectKey
internal/model/fetcher.go: Fetcher{ModelStore,ModelLister}. Start/Stop/TriggerRefresh/Refresh/fetchProviderModels. concurrent per-provider fetch, model category inference
internal/model/mapper.go: Mapper{ModelStoreReader}. GetVisibleModels(acc) -> []Model with aliases + thinking variants. ResolveModel(acc,clientName) -> (realName,*Model,error). ReverseMapModel(acc,realName) -> alias

internal/provider/provider.go: Provider struct, ProviderType constants, NextKey() mutex-protected round-robin
internal/provider/registry.go: Registry{ProviderStore}. Create/List/Get/Update/ExistsByBaseURL/Delete

internal/proxy/proxy.go: Proxy{registry,mapper,http.Client}. HandleChatCompletions/Completions/Embeddings/ImageGenerations/AudioTranscriptions/AudioSpeech. handleStreamableProxy/handleSimpleProxy. proxyStreamResponse/proxySimpleResponse. replaceModelInJSON. injectThinking. buildTargetURL.
internal/proxy/sse.go: SSEReader{bufio.Reader}. ReadEvent() -> SSEEvent{Event,Data}

internal/router/router.go: Router{chi.Router}. New(). registerRoutes/registerAdminRoutes. handleListModels + all admin handlers. WebHandler() -> separate chi router for web_listen_addr.

internal/scanner/scanner.go: Scanner{client,ports,ProviderChecker}. Scan()->[]DiscoveredServer. scans all non-loopback /24 subnets, probes ports 8080/8081/8085.

internal/store/store.go: StoreData{Accounts,Providers,Models,Settings}. Settings{ModelRefreshSec,ModelThinking}
internal/store/json_store.go: JSONStore{mu RWMutex,data,dataFile}. Load/Save. All CRUD methods. saveLocked. RLock serialization + lock-free I/O.

internal/version/version.go: Version string, BuildTime string (injected via ldflags, default "dev"/"unknown")

internal/webui/webui.go: Handler() returns http.Handler serving embedded index.html via //go:embed
internal/webui/index.html: single-page admin panel. Pure HTML+CSS+JS. 5 tabs. ADMIN_KEY hardcoded.
```

# DEPENDENCIES

```yaml
go.mod:
  module: api-bridge
  go: 1.26.2
  require:
    github.com/go-chi/chi/v5: v5.2.5  # HTTP router
    github.com/google/uuid: v1.6.0     # UUID generation
    gopkg.in/yaml.v3: v3.0.1           # YAML config parsing
  # No database driver, no CGO, no external services
```

# CHANGELOG_V1_0

```yaml
v1.0 changes from baseline:
  - provider.go: added keyMu sync.Mutex, protected NextKey() critical section (was data race)
  - json_store.go: refactored all 9 write methods from Lock+deferUnlock+I/O to Lock->mutate->Unlock->saveLocked
  - json_store.go: saveLocked changed from lock-free to RLock serialization + lock-free I/O
  - internal/version/version.go: new package for ldflags version injection
  - cmd/bridge/main.go: imports version, prints version+BuildTime on startup
```
