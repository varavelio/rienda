package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxErrorBodyBytes caps failure payloads kept for error reporting.
const maxErrorBodyBytes = 64 * 1024

// postJSON sends payload as a JSON POST and decodes a 2xx response into out.
func postJSON(
	ctx context.Context,
	client *http.Client,
	provider, url string,
	payload, out any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("provider: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("provider: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("provider: send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return apiErrorFromResponse(provider, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("provider: decode response: %w", err)
	}
	return nil
}

// postStream sends payload as a JSON POST and returns the SSE body of a 2xx
// response. The caller owns the returned body.
func postStream(
	ctx context.Context,
	client *http.Client,
	provider, url string,
	payload any,
) (io.ReadCloser, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("provider: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider: send request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := apiErrorFromResponse(provider, resp)
		_ = resp.Body.Close()
		return nil, apiErr
	}
	return resp.Body, nil
}

// apiErrorFromResponse reads a failed response and maps it to an *llm.Error.
func apiErrorFromResponse(provider string, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return newError(provider, resp.StatusCode, "", "", http.StatusText(resp.StatusCode))
	}
	return apiError(provider, resp.StatusCode, body)
}
