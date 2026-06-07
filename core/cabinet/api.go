package cabinet

import "context"

// API provides the public interface for the Cabinet service.
type API struct {
	Cabinet
}

// NewAPI creates a new API instance.
func NewAPI(svc Cabinet) *API {
	return &API{svc}
}

// Close closes the underlying resources, like the WASM runtime.
func (a *API) Close(ctx context.Context) error {
	return a.Cabinet.Close(ctx)
}
