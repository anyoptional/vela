package jinx

import (
	"errors"
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
	discovery         DiscoveryClient
	lb                LoadBalancer
	perAttemptTimeout time.Duration
	maxAttempts       int
	cooldown          time.Duration
	logger            *slog.Logger
	httpClient        *http.Client
	attemptPolicies   []AttemptPolicy
	transport         Transport
}

// Option configures a Client.
type Option func(*config)

// Client sends requests to its configured target.
type Client struct {
	target    Target
	discovery DiscoveryClient
	lb        LoadBalancer
	cfg       config
	logger    *slog.Logger
	cool      cooldownTracker
	transport Transport
}

// New constructs a Client for the supplied target and options.
func New(target Target, opts ...Option) (*Client, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}

	cfg := config{
		lb:                newRoundRobin(),
		perAttemptTimeout: defaultPerAttemptTimeout,
		maxAttempts:       defaultMaxAttempts,
		cooldown:          defaultCooldown,
		logger:            slog.Default(),
		httpClient:        &http.Client{},
		attemptPolicies:   defaultAttemptPolicies(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if target.Mode() == TargetService && cfg.discovery == nil {
		return nil, errors.New("jinx: service target requires a discovery client")
	}
	if target.Mode() == TargetDirect {
		cfg.discovery = nil
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
	if cfg.transport == nil {
		cfg.transport = newRestyTransport(cfg, cfg.logger)
	}

	return &Client{
		target:    target,
		discovery: cfg.discovery,
		lb:        cfg.lb,
		cfg:       cfg,
		logger:    cfg.logger,
		cool:      newCooldownTracker(cfg.cooldown),
		transport: cfg.transport,
	}, nil
}

// WithDiscovery sets the service discovery source.
func WithDiscovery(discovery DiscoveryClient) Option {
	return func(cfg *config) {
		cfg.discovery = discovery
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

// NewRequest creates a request builder for the configured target and route.
func (c *Client) NewRequest(method, path string) *Request {
	return &Request{
		method: method,
		path:   path,
		c:      c,
	}
}
