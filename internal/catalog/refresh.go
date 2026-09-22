package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// entry is the subset of a model entry of the database Rienda reads.
type entry struct {
	Limit struct {
		Context int `json:"context"`
	} `json:"limit"`
}

// Refresh fetches the model database and replaces the cache file with the
// reduced map it holds. The new content is written to a temporary file that is
// renamed over the cache, so a failed or interrupted refresh leaves the
// previous cache exactly as it was. The returned error describes a failure
// that callers of a best-effort refresh may ignore: the resolution simply
// falls back.
func (c *Catalog) Refresh(ctx context.Context) error {
	models, err := c.fetch(ctx)
	if err != nil {
		return err
	}
	if err := c.store(cache{RefreshedAt: c.now().UTC(), Models: models}); err != nil {
		return err
	}
	return nil
}

// Run keeps the cache current until ctx is canceled. The first pass is
// immediate, so a mode that starts the loop gets a cache without waiting, and
// every later pass happens once per interval and refreshes only when the cache
// is missing or expired.
//
// Run blocks and is meant to be started in a goroutine, which every mode does
// while it drives a session and cancels when it returns.
func (c *Catalog) Run(ctx context.Context) {
	c.refreshIfStale(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.after(c.interval):
			c.refreshIfStale(ctx)
		}
	}
}

// refreshIfStale refreshes the cache when it is missing or expired, and does
// nothing while it is fresh. The failure of a refresh is discarded, because a
// run must never fail nor wait on the network.
func (c *Catalog) refreshIfStale(ctx context.Context) {
	if c.fresh() {
		return
	}
	_ = c.Refresh(ctx)
}

// fresh reports whether the cache exists and was refreshed within its
// lifetime.
func (c *Catalog) fresh() bool {
	loaded, err := c.load()
	if err != nil {
		return false
	}
	return c.now().Sub(loaded.RefreshedAt) < cacheTTL
}

// fetch downloads the model database and reduces it to the map Rienda caches.
func (c *Catalog) fetch(ctx context.Context) (map[string]model, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("catalog: build request for %s: %w", c.endpoint, err)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("catalog: fetch %s: %w", c.endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"catalog: fetch %s: unexpected status %s",
			c.endpoint,
			response.Status,
		)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("catalog: read %s: %w", c.endpoint, err)
	}
	return reduce(data)
}

// reduce turns the model database into the reduced map Rienda caches: the
// facts of every model, keyed by the normalized last segment of its
// identifier.
func reduce(data []byte) (map[string]model, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("catalog: decode database: %w", err)
	}
	// The endpoint publishes a flat map of model entries. Accepting the
	// "models" wrapper as well keeps the reduction working should the endpoint
	// ever nest them.
	if wrapped, found := document["models"]; found {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(wrapped, &nested); err == nil {
			document = nested
		}
	}

	models := make(map[string]model, len(document))
	for id, raw := range document {
		var declared entry
		if err := json.Unmarshal(raw, &declared); err != nil {
			continue
		}
		key := modelKey(id)
		if key == "" || declared.Limit.Context <= 0 {
			continue
		}
		models[key] = model{ContextWindow: declared.Limit.Context}
	}
	return models, nil
}

// store replaces the cache file with next atomically: the content is written
// to a temporary file in the same directory and renamed over the cache, so an
// interrupted write never damages the previous cache.
func (c *Catalog) store(next cache) error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("catalog: create cache directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("catalog: encode cache: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(dir, cacheFileName+".*")
	if err != nil {
		return fmt.Errorf("catalog: create temporary cache: %w", err)
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("catalog: write temporary cache: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("catalog: close temporary cache: %w", err)
	}
	if err := os.Rename(temp, c.path); err != nil {
		return fmt.Errorf("catalog: replace cache %s: %w", c.path, err)
	}
	return nil
}
