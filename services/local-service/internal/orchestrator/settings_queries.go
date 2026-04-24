package orchestrator

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cialloclaw/cialloclaw/services/local-service/internal/model"
	"github.com/cialloclaw/cialloclaw/services/local-service/internal/storage"
)

// SettingsGet handles agent.settings.get.
func (s *Service) SettingsGet(params map[string]any) (map[string]any, error) {
	settings := normalizeSettingsSnapshot(s.runEngine.Settings())
	scope := normalizeSettingsScope(stringValue(params, "scope", "all"))
	if scope == "all" || scope == "models" {
		settingsWithSecrets, err := s.attachSensitiveSettingAvailability(settings)
		if err != nil {
			return nil, err
		}
		settings = settingsWithSecrets
	}
	if scope == "all" {
		return map[string]any{"settings": settings}, nil
	}

	section, ok := settings[scope].(map[string]any)
	if !ok {
		return map[string]any{"settings": map[string]any{}}, nil
	}

	return map[string]any{"settings": map[string]any{scope: cloneMap(section)}}, nil
}

// SettingsUpdate handles agent.settings.update and returns the effective
// settings patch plus apply-mode metadata.
func (s *Service) SettingsUpdate(params map[string]any) (map[string]any, error) {
	normalizedParams := normalizeSettingsUpdateParams(params)
	modelSecretTouched := false
	secretUpdatedKeys := make([]string, 0, 2)
	if models := cloneMap(mapValue(normalizedParams, "models")); len(models) > 0 {
		if deleteAPIKey := boolValue(models, "delete_api_key", false); deleteAPIKey {
			provider := s.providerForSettingsUpdate(models)
			if err := s.deleteModelSecret(provider); err != nil {
				return nil, err
			}
			delete(models, "delete_api_key")
			normalizedParams["models"] = models
			modelSecretTouched = true
			secretUpdatedKeys = append(secretUpdatedKeys, "models.delete_api_key")
		}
		if apiKey := stringValue(models, "api_key", ""); apiKey != "" {
			provider := s.providerForSettingsUpdate(models)
			if err := s.persistModelSecret(provider, apiKey); err != nil {
				return nil, err
			}
			delete(models, "api_key")
			normalizedParams["models"] = models
			modelSecretTouched = true
			secretUpdatedKeys = append(secretUpdatedKeys, "models.api_key")
		}
	}
	effectiveSettings, updatedKeys, applyMode, needRestart, err := s.runEngine.UpdateSettings(normalizedParams)
	if err != nil {
		return nil, err
	}
	if modelSettingsRequireRestart(normalizedParams, secretUpdatedKeys) {
		// The active model service is still constructed at bootstrap time, so
		// provider/credential/base-url/model changes must surface as restart
		// required until hot-reload semantics are implemented across the runtime.
		applyMode = "restart_required"
		needRestart = true
	}
	if modelSecretTouched {
		if _, ok := effectiveSettings["models"]; !ok {
			effectiveSettings["models"] = map[string]any{}
		}
	}
	if _, ok := effectiveSettings["models"]; ok {
		effectiveSettingsWithSecrets, err := s.attachSensitiveSettingAvailability(effectiveSettings)
		if err != nil {
			return nil, err
		}
		effectiveSettings = effectiveSettingsWithSecrets
	}
	effectiveSettings = outwardSettingsUpdatePatch(effectiveSettings)
	updatedKeys = outwardSettingsUpdateKeys(updatedKeys, secretUpdatedKeys)
	return map[string]any{
		"updated_keys":       updatedKeys,
		"effective_settings": effectiveSettings,
		"apply_mode":         applyMode,
		"need_restart":       needRestart,
	}, nil
}

func modelSettingsRequireRestart(normalizedParams map[string]any, secretUpdatedKeys []string) bool {
	for _, key := range secretUpdatedKeys {
		if key == "models.api_key" || key == "models.delete_api_key" {
			return true
		}
	}
	models := cloneMap(mapValue(normalizedParams, "models"))
	if len(models) == 0 {
		return false
	}
	for _, key := range []string{"provider", "base_url", "model"} {
		if _, ok := models[key]; ok {
			return true
		}
	}
	return false
}

func (s *Service) attachSensitiveSettingAvailability(settings map[string]any) (map[string]any, error) {
	cloned := normalizeSettingsSnapshot(cloneMap(settings))
	if cloned == nil {
		cloned = map[string]any{}
	}
	models := cloneMap(mapValue(cloned, "models"))
	if models == nil {
		models = map[string]any{}
	}
	credentials := cloneMap(mapValue(models, "credentials"))
	if credentials == nil {
		credentials = map[string]any{}
	}
	provider, configured, err := s.modelSecretConfigured(providerFromSettings(models, s.defaultSettingsProvider()))
	if err != nil {
		return nil, err
	}
	if stringValue(models, "provider", "") == "" && provider != "" {
		models["provider"] = provider
	}
	credentials["provider_api_key_configured"] = configured
	if stronghold := strongholdStatusFromStorage(s.storage); len(stronghold) > 0 {
		credentials["stronghold"] = stronghold
	}
	models["credentials"] = credentials
	cloned["models"] = models
	return cloned, nil
}

func (s *Service) modelSecretConfigured(provider string) (string, bool, error) {
	resolvedProvider := firstNonEmptyString(strings.TrimSpace(provider), s.defaultSettingsProvider())
	if s.storage == nil || s.storage.SecretStore() == nil || resolvedProvider == "" {
		return resolvedProvider, false, nil
	}
	_, err := s.storage.SecretStore().GetSecret(context.Background(), "model", resolvedProvider+"_api_key")
	if err == nil {
		return resolvedProvider, true, nil
	}
	if errors.Is(err, storage.ErrSecretNotFound) {
		return resolvedProvider, false, nil
	}
	if errors.Is(err, storage.ErrSecretStoreAccessFailed) {
		return resolvedProvider, false, ErrStrongholdAccessFailed
	}
	if errors.Is(err, storage.ErrStrongholdUnavailable) {
		return resolvedProvider, false, ErrStrongholdAccessFailed
	}
	return resolvedProvider, false, err
}

func (s *Service) persistModelSecret(provider, apiKey string) error {
	resolvedProvider := firstNonEmptyString(strings.TrimSpace(provider), s.defaultSettingsProvider())
	if s.storage == nil || s.storage.SecretStore() == nil || resolvedProvider == "" {
		return ErrStrongholdAccessFailed
	}
	if err := s.storage.SecretStore().PutSecret(context.Background(), storage.SecretRecord{
		Namespace: "model",
		Key:       resolvedProvider + "_api_key",
		Value:     strings.TrimSpace(apiKey),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		normalizedErr := storage.NormalizeSecretStoreError(err)
		if errors.Is(normalizedErr, storage.ErrStrongholdAccessFailed) || errors.Is(normalizedErr, storage.ErrStrongholdUnavailable) || errors.Is(normalizedErr, storage.ErrSecretStoreAccessFailed) {
			return ErrStrongholdAccessFailed
		}
		return normalizedErr
	}
	return nil
}

func (s *Service) deleteModelSecret(provider string) error {
	resolvedProvider := firstNonEmptyString(strings.TrimSpace(provider), s.defaultSettingsProvider())
	if s.storage == nil || s.storage.SecretStore() == nil || resolvedProvider == "" {
		return ErrStrongholdAccessFailed
	}
	if err := s.storage.SecretStore().DeleteSecret(context.Background(), "model", resolvedProvider+"_api_key"); err != nil {
		normalizedErr := storage.NormalizeSecretStoreError(err)
		if errors.Is(normalizedErr, storage.ErrStrongholdAccessFailed) || errors.Is(normalizedErr, storage.ErrStrongholdUnavailable) || errors.Is(normalizedErr, storage.ErrSecretStoreAccessFailed) {
			return ErrStrongholdAccessFailed
		}
		return normalizedErr
	}
	return nil
}

func strongholdStatusFromStorage(store *storage.Service) map[string]any {
	if store == nil || store.Stronghold() == nil {
		return map[string]any{
			"backend":      "none",
			"available":    false,
			"fallback":     false,
			"initialized":  false,
			"formal_store": false,
		}
	}
	descriptor := store.Stronghold().Descriptor()
	return map[string]any{
		"backend":      descriptor.Backend,
		"available":    descriptor.Available,
		"fallback":     descriptor.Fallback,
		"initialized":  descriptor.Initialized,
		"formal_store": descriptor.Available && !descriptor.Fallback,
	}
}

func normalizeSettingsScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "", "all":
		return "all"
	case "data_log":
		return "models"
	default:
		return strings.TrimSpace(scope)
	}
}

func normalizeSettingsSnapshot(settings map[string]any) map[string]any {
	cloned := cloneMap(settings)
	if cloned == nil {
		return map[string]any{}
	}
	models := cloneMap(mapValue(cloned, "models"))
	if models == nil {
		models = map[string]any{}
	}
	if legacy := cloneMap(mapValue(cloned, "data_log")); len(legacy) > 0 {
		for key, value := range legacy {
			if key == "provider" {
				models[key] = value
				continue
			}
			credentials := cloneMap(mapValue(models, "credentials"))
			if credentials == nil {
				credentials = map[string]any{}
			}
			credentials[key] = value
			models["credentials"] = credentials
		}
		delete(cloned, "data_log")
	}
	models = normalizeModelSettingsSection(models)
	if len(models) > 0 {
		cloned["models"] = models
	}
	return cloned
}

func normalizeSettingsUpdateParams(params map[string]any) map[string]any {
	cloned := cloneMap(params)
	if cloned == nil {
		return map[string]any{}
	}
	models := cloneMap(mapValue(cloned, "models"))
	if models == nil {
		models = map[string]any{}
	}
	if legacy := cloneMap(mapValue(cloned, "data_log")); len(legacy) > 0 {
		for key, value := range legacy {
			if key == "provider_api_key_configured" || key == "stronghold" {
				continue
			}
			models[key] = value
		}
		delete(cloned, "data_log")
	}
	if credentials := cloneMap(mapValue(models, "credentials")); len(credentials) > 0 {
		for key, value := range credentials {
			if key == "provider_api_key_configured" || key == "stronghold" {
				continue
			}
			models[key] = value
		}
		delete(models, "credentials")
	}
	if len(models) > 0 {
		cloned["models"] = models
	}
	return cloned
}

func normalizeModelSettingsSection(models map[string]any) map[string]any {
	cloned := cloneMap(models)
	if cloned == nil {
		cloned = map[string]any{}
	}
	credentials := cloneMap(mapValue(cloned, "credentials"))
	if credentials == nil {
		credentials = map[string]any{}
	}
	for _, key := range []string{"budget_auto_downgrade", "base_url", "model", "budget_policy"} {
		if value, ok := cloned[key]; ok {
			credentials[key] = value
			delete(cloned, key)
		}
	}
	if len(credentials) > 0 {
		cloned["credentials"] = credentials
	}
	return cloned
}

func modelSettingsSection(settings map[string]any) map[string]any {
	return cloneMap(mapValue(normalizeSettingsSnapshot(settings), "models"))
}

func modelCredentialSettings(settings map[string]any) map[string]any {
	return cloneMap(mapValue(modelSettingsSection(settings), "credentials"))
}

func outwardSettingsUpdatePatch(settings map[string]any) map[string]any {
	cloned := normalizeSettingsSnapshot(settings)
	models := cloneMap(mapValue(cloned, "models"))
	if len(models) == 0 {
		return cloned
	}
	credentials := cloneMap(mapValue(models, "credentials"))
	delete(models, "credentials")
	for _, key := range []string{"budget_auto_downgrade", "provider_api_key_configured", "base_url", "model", "stronghold"} {
		if value, ok := credentials[key]; ok {
			models[key] = value
		}
	}
	cloned["models"] = models
	return cloned
}

func outwardSettingsUpdateKeys(internalKeys, secretUpdatedKeys []string) []string {
	seen := make(map[string]struct{}, len(internalKeys)+len(secretUpdatedKeys))
	result := make([]string, 0, len(internalKeys)+len(secretUpdatedKeys))
	for _, key := range internalKeys {
		mapped := key
		if strings.HasPrefix(mapped, "models.credentials.") {
			mapped = "models." + strings.TrimPrefix(mapped, "models.credentials.")
		}
		if _, ok := seen[mapped]; ok {
			continue
		}
		seen[mapped] = struct{}{}
		result = append(result, mapped)
	}
	for _, key := range secretUpdatedKeys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (s *Service) providerForSettingsUpdate(models map[string]any) string {
	merged := modelSettingsSection(s.runEngine.Settings())
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range normalizeModelSettingsSection(models) {
		merged[key] = value
	}
	return providerFromSettings(merged, s.defaultSettingsProvider())
}

func (s *Service) defaultSettingsProvider() string {
	if s.model == nil {
		return ""
	}
	return strings.TrimSpace(s.model.Provider())
}

func providerFromSettings(models map[string]any, fallback string) string {
	provider := firstNonEmptyString(stringValue(models, "provider", ""), fallback)
	if provider == "openai" {
		return model.OpenAIResponsesProvider
	}
	return provider
}
