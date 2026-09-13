package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/domain"
)

func (s *collection) get(ctx context.Context, endpoint string, out any) (http.Header, *Error) {
	if err := ctx.Err(); err != nil {
		return nil, contextFailure(err)
	}
	requestLimit, byteLimit := maxRequests, maxTotalResponseBytes
	// Keep enough capacity for GitHub's final canonical-identity check even if
	// selected paths exhaust their collection budgets.
	if s.c.binding.Provider == "github" && !s.finalIdentity {
		requestLimit--
		byteLimit -= maxResponseBytes
	}
	if s.requests >= requestLimit {
		return nil, failure("request_limit")
	}
	remaining := byteLimit - s.responseBytes
	if remaining <= 0 {
		return nil, failure("response_limit")
	}
	if err := s.authorize(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err())
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, contextFailure(err)
		}
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrUnauthenticated) || errors.Is(err, domain.ErrCollectionStopped) || errors.Is(err, domain.ErrNotFound) {
			return nil, failure("access_revoked")
		}
		return nil, &Error{Code: "authorization_unavailable", Retryable: true}
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.c.origin+endpoint, nil)
	if err != nil {
		return nil, failure("invalid_input")
	}
	req.Header.Set("Accept-Encoding", "identity")
	if s.c.binding.Provider == "github" {
		req.Header.Set("Authorization", "Bearer "+s.c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	} else {
		req.Header.Set("PRIVATE-TOKEN", s.c.token)
		req.Header.Set("Accept", "application/json")
	}
	s.requests++
	resp, err := s.c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err())
		}
		return nil, &Error{Code: "transport", Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusFailure(resp.StatusCode, resp.Header)
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, failure("invalid_response")
	}
	limit := min(maxResponseBytes, remaining)
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	s.responseBytes += len(b)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err())
		}
		return nil, &Error{Code: "transport", Retryable: true}
	}
	if len(b) > limit {
		return nil, failure("response_limit")
	}
	if !utf8.Valid(b) {
		return nil, failure("invalid_response")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, failure("invalid_response")
	}
	return resp.Header, nil
}

func contextFailure(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("deadline")
	}
	return failure("cancelled")
}
func statusFailure(status int, headers http.Header) *Error {
	if status == 429 || status == 403 && (headers.Get("Retry-After") != "" || headers.Get("X-RateLimit-Remaining") == "0") {
		return &Error{Code: "rate_limited", Retryable: true, RetryAfter: retryDelay(headers)}
	}
	if status >= 500 && status <= 599 || status == 408 {
		// No provider delay means Temporal owns the ordinary retry backoff. A
		// synthetic rate-limit fallback here would incorrectly turn a 5xx into
		// a refusal to retry when that fallback exceeds the workflow's policy.
		var delay time.Duration
		if headers.Get("Retry-After") != "" {
			delay = retryDelay(headers)
		}
		return &Error{Code: "provider_unavailable", Retryable: true, RetryAfter: delay}
	}
	switch {
	case status >= 300 && status <= 399:
		return failure("redirect")
	case status == 401 || status == 403:
		return failure("provider_denied")
	case status == 404:
		return failure("not_found")
	default:
		return failure("invalid_response")
	}
}
func retryDelay(headers http.Header) time.Duration {
	delay := time.Minute
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// Clamp before converting seconds so malicious integers cannot overflow.
		delay = time.Duration(min(seconds, 300)) * time.Second
	} else if date, err := http.ParseTime(value); err == nil {
		delay = time.Until(date)
	} else if value == "" && headers.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 && seconds < 1<<40 {
			delay = time.Until(time.Unix(seconds, 0))
		}
	}
	return max(time.Second, min(delay, 5*time.Minute))
}
