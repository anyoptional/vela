package vela

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-logr/logr"
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
	connectTimeout    time.Duration
	maxAttempts       int
	cooldown          time.Duration
	logger            logr.Logger
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
	logger    logr.Logger
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
		logger:            defaultLogger(),
		attemptPolicies:   defaultAttemptPolicies(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if target.Mode() == TargetService && cfg.discovery == nil {
		return nil, errors.New("vela: service target requires a discovery client")
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
	if cfg.connectTimeout < 0 {
		cfg.connectTimeout = 0
	}
	if cfg.httpClient == nil {
		cfg.httpClient = newDefaultHTTPClient(cfg.connectTimeout)
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

// WithTimeout sets the client-side default timeout for each request attempt.
// A request without its own timeout inherits this value.
func WithTimeout(perAttempt time.Duration) Option {
	return func(cfg *config) {
		cfg.perAttemptTimeout = perAttempt
	}
}

// WithConnectTimeout sets the dial timeout of the default HTTP client. It
// has no effect when WithHTTPClient or WithTransport supplies a custom
// transport: those own their dial behavior.
func WithConnectTimeout(connect time.Duration) Option {
	return func(cfg *config) {
		cfg.connectTimeout = connect
	}
}

// newDefaultHTTPClient builds the default *http.Client. When connectTimeout
// is positive, the transport dialer enforces it as a connect timeout.
func newDefaultHTTPClient(connectTimeout time.Duration) *http.Client {
	httpClient := &http.Client{}
	if connectTimeout > 0 {
		httpClient.Transport = &http.Transport{
			DialContext: (&net.Dialer{Timeout: connectTimeout}).DialContext,
		}
	}
	return httpClient
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
func WithLogger(l logr.Logger) Option {
	return func(cfg *config) {
		cfg.logger = l
	}
}

// defaultLogger writes to stdout through logr.FromSlogHandler.
func defaultLogger() logr.Logger {
	return logr.FromSlogHandler(slog.Default().Handler())
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
