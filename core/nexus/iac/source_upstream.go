package iac

import (
	"context"
	"fmt"
)

// UpstreamSource implements IaCSource for fetching config from an upstream Relay API.
type UpstreamSource struct {
	APIURL string
	Token  string
}

// NewUpstreamSource creates a new UpstreamSource.
func NewUpstreamSource(apiURL, token string) *UpstreamSource {
	return &UpstreamSource{
		APIURL: apiURL,
		Token:  token,
	}
}

// FetchConfig calls the upstream API to get the configuration.
func (us *UpstreamSource) FetchConfig(ctx context.Context) (map[string][]byte, error) {
	// TODO: Implement HTTP/gRPC client call
	return nil, fmt.Errorf("upstream source not implemented yet")
}
