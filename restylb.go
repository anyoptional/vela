package vela

import (
	"context"
	"errors"
	"net"
	"strconv"

	"resty.dev/v3"
)

// instanceBaseURL returns the base URL (scheme://host:port) of an instance.
func instanceBaseURL(ins Instance) string {
	scheme := "http"
	if ins.Secure {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(ins.Host, strconv.Itoa(ins.Port))
}

// attemptSelection carries the base URL the Do loop picked for one attempt.
// The Do loop keeps full control of instance selection (tried set, cooldown,
// failover semantics); the resty LoadBalancer adapter below hands that choice
// to resty so it composes the final URL from base URL + relative path.
type attemptSelection struct {
	base   string
	chosen Instance
}

type attemptSelectionKey struct{}

// restyLoadBalancer bridges vela's per-attempt instance selection into
// resty's LoadBalancer interface. It is stateless: the chosen base URL
// travels in the request context, so one adapter serves the whole client.
type restyLoadBalancer struct{}

func (restyLoadBalancer) NextWithContext(ctx context.Context) (string, error) {
	state, ok := ctx.Value(attemptSelectionKey{}).(*attemptSelection)
	if !ok || state == nil || state.base == "" {
		return "", errors.New("vela: no attempt selection in context")
	}
	return state.base, nil
}

func (restyLoadBalancer) Feedback(*resty.RequestFeedback) {
	// vela applies cooldown in the Do loop with its own semantics: only
	// transport errors cool an instance, while resty also reports 5xx as
	// failure here.
}

func (restyLoadBalancer) Close() error { return nil }

var _ resty.LoadBalancer = restyLoadBalancer{}
