package cabinet

// API provides the public entry points for the cabinet service.
// It embeds the Cabinet interface to provide access to the underlying stores
// and business logic in a controlled way.
type API struct {
	Cabinet
}

// NewAPI creates a new API instance that wraps the given Cabinet service.
func NewAPI(c Cabinet) *API {
	return &API{Cabinet: c}
}
