package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// apiRequest performs an authenticated call against the Vesta API and returns the raw
// response body.
//
// The older subcommands each hand-roll this; new ones share it so that error reporting
// stays consistent — in particular so a non-2xx status surfaces the server's message
// rather than being decoded as if it were the expected payload.
func apiRequest(method, path string, body interface{}) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, apiURL+path, reader)
	if err != nil {
		return nil, err
	}
	if apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+apiToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Message string `json:"message"`
			Details string `json:"details"`
		}
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Message != "" {
			if apiErr.Details != "" {
				return nil, fmt.Errorf("%s (%s)", apiErr.Message, apiErr.Details)
			}
			return nil, fmt.Errorf("%s", apiErr.Message)
		}
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, string(raw))
	}

	return raw, nil
}
