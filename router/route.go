package router

import (
	"hash/fnv"
)

var (
	// DefaultLink is default network link
	DefaultLink = "local"
	// DefaultLocalMetric is default route cost for a local route
	DefaultLocalMetric int64 = 1
)

// Route is network route
type Route struct {
	// Service is destination service name
	Service string
	// Service Version
	Version string
	// Address is service node address
	Address string
	// Gateway is route gateway
	Gateway string
	// Network is network address
	Network string
	// Router is router id
	Router string
	// Link is network link
	Link string
	// Metric is the route cost metric
	Metric int64
	// Metadata for the route
	Metadata map[string]string
}

// Hash returns route hash sum.
func (r *Route) Hash() uint64 {
	h := fnv.New64()
	h.Reset()
	h.Write([]byte(r.Service + r.Version + r.Address + r.Gateway + r.Network + r.Router + r.Link))
	return h.Sum64()
}
