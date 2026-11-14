package jinx

// Instance describes one discovered endpoint for a service.
type Instance struct {
	ID        string
	ServiceID string
	Host      string
	Port      int
	Secure    bool
	Metadata  map[string]string
}
