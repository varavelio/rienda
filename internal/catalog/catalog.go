package catalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Names of the directories and the file the catalog lives in.
const (
	// riendaDirName is the directory inside the home directory that holds
	// everything Rienda stores for a user.
	riendaDirName = ".rienda"

	// cacheDirName is the directory under the Rienda directory that groups the
	// caches of every feature.
	cacheDirName = "cache"

	// cacheFileName is the name of the cache file inside the cache directory.
	cacheFileName = "models.json"
)

// endpointEnvVar names the environment variable that overrides the URL of the
// model database, the same way RIENDA_CONFIG overrides the configuration path.
const endpointEnvVar = "RIENDA_CATALOG_URL"

// DefaultEndpoint is the URL of the models.dev database Rienda refreshes its
// cache from.
const DefaultEndpoint = "https://models.dev/models.json"

// FallbackWindow is the context window used for a model nobody declares and
// nobody knows. It errs low on purpose, so the compaction arrives early rather
// than late.
const FallbackWindow = 96_000

// Defaults of the refresh mechanism.
const (
	// cacheTTL is how long a cached database is considered fresh. An expired
	// cache is still used: a stale entry is far better than no entry.
	cacheTTL = 4 * time.Hour

	// refreshInterval is how often the background loop checks the cache.
	refreshInterval = 5 * time.Minute

	// requestTimeout bounds one fetch, so a hanging endpoint can never leave a
	// refresh in flight forever.
	requestTimeout = 30 * time.Second
)

// Options configures a Catalog.
type Options struct {
	// Dir overrides the cache directory. It defaults to the cache directory of
	// the user home.
	Dir string

	// Endpoint overrides the URL of the model database. It defaults to the
	// RIENDA_CATALOG_URL environment variable and then to DefaultEndpoint.
	Endpoint string

	// Client is the HTTP client the fetch uses. It defaults to a client that
	// bounds a request with requestTimeout.
	Client *http.Client

	// Interval overrides how often the background loop checks the cache. It
	// defaults to refreshInterval.
	Interval time.Duration

	// Now returns the current time. It defaults to time.Now and lets tests
	// control the age of a cache.
	Now func() time.Time

	// After waits for a refresh interval. It defaults to time.After and lets
	// tests drive the background loop without sleeping.
	After func(time.Duration) <-chan time.Time
}

// Catalog resolves the context window of a model from a cached copy of the
// models.dev database.
//
// A Catalog is safe for concurrent use: every lookup reads the cache file, and
// only the background loop writes it.
type Catalog struct {
	path     string
	endpoint string
	client   *http.Client
	interval time.Duration
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time
}

// cache is the reduced content of the model database: the context window of
// every model and the moment the cache was refreshed.
type cache struct {
	// RefreshedAt is the moment the cache was last written.
	RefreshedAt time.Time `json:"refreshedAt"`

	// Models maps the last segment of a model identifier to its context
	// window, because the context is the only fact Rienda uses.
	Models map[string]int `json:"models"`
}

// New builds a catalog. It resolves the cache directory and the endpoint from
// the options, the environment and the defaults, in that order.
func New(opts Options) (*Catalog, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("catalog: locate home directory: %w", err)
		}
		dir = filepath.Join(home, riendaDirName, cacheDirName)
	}

	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv(endpointEnvVar))
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = refreshInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	after := opts.After
	if after == nil {
		after = time.After
	}

	return &Catalog{
		path:     filepath.Join(dir, cacheFileName),
		endpoint: endpoint,
		client:   client,
		interval: interval,
		now:      now,
		after:    after,
	}, nil
}

// Resolve returns the context window of a model from the cache, and whether it
// was found. Models are matched by the last segment of their identifier,
// ignoring the provider, because the database names them in path style while a
// configuration names them freely. A missing, corrupt or expired cache simply
// resolves nothing: it is never an error.
func (c *Catalog) Resolve(modelID string) (int, bool) {
	key := lastSegment(modelID)
	if key == "" {
		return 0, false
	}

	loaded, err := c.load()
	if err != nil {
		return 0, false
	}
	window, found := loaded.Models[key]
	if !found || window <= 0 {
		return 0, false
	}
	return window, true
}

// Window returns the context window of a model, resolved from three sources in
// strict order: the value declared by the configuration, the value the
// catalog knows, and FallbackWindow. Which source answered is never surfaced.
func (c *Catalog) Window(modelID string, declared int) int {
	if declared > 0 {
		return declared
	}
	if window, found := c.Resolve(modelID); found {
		return window
	}
	return FallbackWindow
}

// load reads the cache file.
func (c *Catalog) load() (cache, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return cache{}, fmt.Errorf("catalog: read cache %s: %w", c.path, err)
	}

	var loaded cache
	if err := json.Unmarshal(data, &loaded); err != nil {
		return cache{}, fmt.Errorf("catalog: decode cache %s: %w", c.path, err)
	}
	return loaded, nil
}

// lastSegment returns the part of a model identifier that follows its last
// slash, which is what makes a path-style database key and a freely named
// configuration model match.
func lastSegment(modelID string) string {
	trimmed := strings.TrimSpace(modelID)
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	return strings.TrimSpace(trimmed)
}
