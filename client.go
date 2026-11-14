package jinx

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultPerAttemptTimeout = 5 * time.Second
	defaultMaxAttempts       = 3
	defaultCooldown          = 10 * time.Second
)

type config struct {
	lb                LoadBalancer
	perAttemptTimeout time.Duration
	maxAttempts       int
	cooldown          time.Duration
	logger            *slog.Logger
	httpClient        *http.Client
}

// Option configures a Client.
type Option func(*config)

// Client sends requests to discovered service instances.
type Client struct {
	discovery  DiscoveryClient
	lb         LoadBalancer
	cfg        config
	httpClient *http.Client
	logger     *slog.Logger
	cool       cooldownTracker
	transport  transport
}

// New constructs a Client with the supplied discovery source and options.
func New(discovery DiscoveryClient, opts ...Option) *Client {
	cfg := config{
		lb:                newRoundRobin(),
		perAttemptTimeout: defaultPerAttemptTimeout,
		maxAttempts:       defaultMaxAttempts,
		cooldown:          defaultCooldown,
		logger:            slog.Default(),
		httpClient:        &http.Client{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.lb == nil {
		cfg.lb = newRoundRobin()
	}
	if cfg.perAttemptTimeout <= 0 {
		cfg.perAttemptTimeout = defaultPerAttemptTimeout
	}
	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = defaultMaxAttempts
	}
	if cfg.cooldown < 0 {
		cfg.cooldown = 0
	} else if cfg.cooldown == 0 {
		cfg.cooldown = defaultCooldown
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	if cfg.httpClient == nil {
		cfg.httpClient = &http.Client{}
	}

	return &Client{
		discovery:  discovery,
		lb:         cfg.lb,
		cfg:        cfg,
		httpClient: cfg.httpClient,
		logger:     cfg.logger,
		cool:       newCooldownTracker(cfg.cooldown),
		transport:  newRestyTransport(cfg, cfg.logger),
	}
}

// WithLoadBalancer sets the strategy used to choose an instance.
func WithLoadBalancer(lb LoadBalancer) Option {
	return func(cfg *config) {
		cfg.lb = lb
	}
}

// WithTimeout sets the timeout for each request attempt.
func WithTimeout(perAttempt time.Duration) Option {
	return func(cfg *config) {
		cfg.perAttemptTimeout = perAttempt
	}
}

// WithMaxAttempts sets the maximum number of request attempts.
func WithMaxAttempts(n int) Option {
	return func(cfg *config) {
		cfg.maxAttempts = n
	}
}

// WithCooldown sets the duration an instance is cooled after a transport error.
func WithCooldown(d time.Duration) Option {
	return func(cfg *config) {
		cfg.cooldown = d
	}
}

// WithLogger sets the logger used by the client.
func WithLogger(l *slog.Logger) Option {
	return func(cfg *config) {
		cfg.logger = l
	}
}

// WithHTTPClient sets the HTTP client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *config) {
		cfg.httpClient = c
	}
}

// NewRequest creates a request builder for the given service and route.
func (c *Client) NewRequest(serviceID, method, path string) *Request {
	return &Request{
		serviceID: serviceID,
		method:    method,
		path:      path,
		c:         c,
	}
}
