package jinx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
)

// AttemptAction is the next operation after a request failure.
type AttemptAction uint8

const (
	// AttemptNext lets the next policy in the configured slice decide.
	AttemptNext AttemptAction = iota
	// AttemptRetrySameEndpoint retries the endpoint used by the last attempt.
	AttemptRetrySameEndpoint
	// AttemptFailover selects an untried service instance.
	AttemptFailover
	// AttemptStop ends request attempts.
	AttemptStop
)

// AttemptState describes the failed attempt passed to a policy.
type AttemptState struct {
	Attempt          int
	AttemptLimit     int
	UntriedEndpoints int
	TargetMode       TargetMode
	Replayable       bool
}

// AttemptPolicy decides how a request proceeds after a failure.
type AttemptPolicy func(context.Context, *Failure, AttemptState) (AttemptAction, error)

// Transport performs one HTTP request and returns its response.
type Transport func(context.Context, *http.Request) (*http.Response, error)

// WithAttemptPolicies replaces the client's default attempt policies.
func WithAttemptPolicies(policies []AttemptPolicy) Option {
	copied := slices.Clone(policies)
	return func(cfg *config) {
		cfg.attemptPolicies = slices.Clone(copied)
	}
}

// WithTransport replaces the default HTTP transport.
func WithTransport(transport Transport) Option {
	return func(cfg *config) {
		cfg.transport = transport
	}
}

func defaultAttemptPolicies() []AttemptPolicy {
	return []AttemptPolicy{
		func(_ context.Context, failure *Failure, state AttemptState) (AttemptAction, error) {
			retryableStatus := failure.Kind == KindStatus &&
				failure.StatusCode >= http.StatusInternalServerError &&
				failure.StatusCode <= 599
			if failure.Kind != KindTransport && !retryableStatus {
				return AttemptNext, nil
			}
			if state.TargetMode == TargetDirect {
				return AttemptRetrySameEndpoint, nil
			}
			if state.UntriedEndpoints > 0 {
				return AttemptFailover, nil
			}
			return AttemptStop, nil
		},
	}
}

func decideAttempt(
	ctx context.Context,
	policies []AttemptPolicy,
	failure *Failure,
	state AttemptState,
) (AttemptAction, *Failure) {
	for _, policy := range policies {
		if policy == nil {
			return AttemptStop, invalidPolicyFailure(failure, errors.New("nil attempt policy"))
		}
		action, err := policy(ctx, failure, state)
		if err != nil {
			return AttemptStop, invalidPolicyFailure(failure, fmt.Errorf("attempt policy: %w", err))
		}
		if action == AttemptNext || action == AttemptFailover && state.TargetMode == TargetDirect {
			continue
		}
		if action > AttemptStop {
			return AttemptStop, invalidPolicyFailure(failure, fmt.Errorf("attempt policy returned unknown action %d", action))
		}
		return action, nil
	}
	return AttemptStop, nil
}

func invalidPolicyFailure(original *Failure, cause error) *Failure {
	if original == nil {
		return &Failure{Kind: KindInvalidCall, Err: cause}
	}
	return &Failure{
		Kind:         KindInvalidCall,
		Err:          errors.Join(original, cause),
		clientName:   original.clientName,
		targetMode:   original.targetMode,
		attempt:      original.attempt,
		attemptLimit: original.attemptLimit,
		instanceID:   original.instanceID,
		serviceID:    original.serviceID,
		endpoint:     original.endpoint,
		bodyExcerpt:  original.bodyExcerpt,
	}
}
