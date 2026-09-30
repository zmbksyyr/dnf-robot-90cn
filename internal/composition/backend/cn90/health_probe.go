package cn90

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// NewBackendHealthProbe returns the scheduler readiness probe for the DNF90
// admin health route. Dialing the game port would make the server allocate a
// game session for every probe, so the loopback health endpoint is used
// instead; a nil or empty admin address keeps the default TCP dial.
func NewBackendHealthProbe(adminAddress string) func() bool {
	adminAddress = strings.TrimSpace(adminAddress)
	if adminAddress == "" {
		return nil
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	return func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+adminAddress+"/healthz/ready", nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		return response.StatusCode >= 200 && response.StatusCode < 300
	}
}
