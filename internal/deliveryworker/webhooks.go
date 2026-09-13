package deliveryworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
)

type WebhookStore interface {
	RecordDeliveryWebhook(context.Context, domain.DeliveryTarget, delivery.WebhookEvent) error
}
type WebhookBinding struct {
	WorkspaceID  string `json:"workspaceId"`
	RepositoryID string `json:"repositoryId"`
	Provider     string `json:"provider"`
	Host         string `json:"host"`
	ProviderID   string `json:"providerId"`
	SecretFile   string `json:"secretFile"`
}

func readOperatorFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return nil, domain.ErrInvalidInput
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, domain.ErrInvalidInput
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, domain.ErrInvalidInput
	}
	return raw, nil
}
func LoadWebhookHandler(db WebhookStore, path string) (http.Handler, error) {
	raw, err := readOperatorFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var config struct {
		Webhooks []WebhookBinding `json:"webhooks"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, domain.ErrInvalidInput
	}
	return NewWebhookHandler(db, config.Webhooks)
}
func NewWebhookHandler(db WebhookStore, bindings []WebhookBinding) (http.Handler, error) {
	if db == nil || len(bindings) < 1 || len(bindings) > 100 {
		return nil, domain.ErrInvalidInput
	}
	selected := map[string]WebhookBinding{}
	for _, b := range bindings {
		id, err := strconv.ParseInt(b.ProviderID, 10, 64)
		if domain.ValidateAccessID(b.WorkspaceID) != nil || domain.ValidateAccessID(b.RepositoryID) != nil || !filepath.IsAbs(b.SecretFile) || err != nil || id < 1 || strconv.FormatInt(id, 10) != b.ProviderID || !(b.Provider == "github" && b.Host == "github.com" || b.Provider == "gitlab" && b.Host == "gitlab.com") {
			return nil, domain.ErrInvalidInput
		}
		key := b.Provider + "/" + b.RepositoryID
		if _, ok := selected[key]; ok {
			return nil, domain.ErrInvalidInput
		}
		selected[key] = b
	}
	slots := make(chan struct{}, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/{provider}/{repository}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "webhook unavailable", 503)
			return
		}
		binding, ok := selected[r.PathValue("provider")+"/"+r.PathValue("repository")]
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if !ok || err != nil || media != "application/json" || r.URL.RawQuery != "" {
			http.Error(w, "invalid webhook", 400)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, delivery.MaxWebhookBytes))
		if err != nil {
			http.Error(w, "invalid webhook", 400)
			return
		}
		secret, err := readOperatorFile(binding.SecretFile, 257)
		if err != nil {
			http.Error(w, "webhook unavailable", 503)
			return
		}
		target := domain.DeliveryTarget{WorkspaceID: binding.WorkspaceID, RepositoryID: binding.RepositoryID, Provider: binding.Provider, Host: binding.Host, ProviderID: binding.ProviderID}
		event, err := delivery.VerifyWebhook(target, strings.TrimSuffix(string(secret), "\n"), r.Header, raw)
		if err != nil {
			http.Error(w, "invalid webhook", 401)
			return
		}
		if event.Type == "ping" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if db.RecordDeliveryWebhook(ctx, target, event) != nil {
			http.Error(w, "webhook unavailable", 503)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	return mux, nil
}
