package proxy

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const routeShards = 256

// shard is a single shard of the sharded route map.
type shard struct {
	sync.RWMutex
	m map[string]*UpstreamEntry
}

// UpstreamEntry contains all precomputed pieces needed to serve traffic to a
// backend target — parsed URL and a pre-created ReverseProxy that uses a
// tuned Transport for connection reuse and low latency.
type UpstreamEntry struct {
	TargetURL *url.URL
	Proxy     *httputil.ReverseProxy
	CreatedAt time.Time
}

// ShardedRouteManager holds shards and methods to manipulate them.
type ShardedRouteManager struct {
	shards [routeShards]*shard
	// Optional: telemetry counters, eviction policy fields, etc.
	logRequests bool
}

// NewShardedRouteManager constructs the manager and initializes shards.
func NewShardedRouteManager(logRequests bool) *ShardedRouteManager {
	m := &ShardedRouteManager{logRequests: logRequests}
	for i := 0; i < routeShards; i++ {
		m.shards[i] = &shard{m: make(map[string]*UpstreamEntry)}
	}
	return m
}

// shardIdx computes a small, fast hash and returns the shard index.
func (m *ShardedRouteManager) shardIdx(key string) uint8 {
	var h uint32
	for i := 0; i < len(key); i++ {
		h = h*16777619 ^ uint32(key[i]) // FNV-like mix (fast)
	}
	return uint8(h % routeShards)
}

// AddRoute registers host -> target. target can be "host:port" or "http(s)://host[:port]".
func (m *ShardedRouteManager) AddRoute(host, target string) error {
	// Normalize target into URL
	var raw string
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		raw = target
	} else {
		// default to http for local tunneled endpoints
		raw = "http://" + target
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}

	// Create an optimized Transport for this upstream.
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 250 * time.Millisecond, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   250,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
	}

	// Precreate a ReverseProxy that reuses this transport and streams quickly.
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
			req.Host = u.Host
		},
		Transport:     transport,
		FlushInterval: 10 * time.Millisecond,
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			if m.logRequests {
				log.Printf("proxy error: host=%s upstream=%s err=%v", req.Host, u.String(), err)
			}
			http.Error(rw, "upstream gateway error", http.StatusBadGateway)
		},
		ModifyResponse: func(resp *http.Response) error {
			return nil
		},
	}

	entry := &UpstreamEntry{
		TargetURL: u,
		Proxy:     proxy,
		CreatedAt: time.Now(),
	}

	idx := m.shardIdx(host)
	s := m.shards[idx]
	s.Lock()
	s.m[host] = entry
	s.Unlock()

	if m.logRequests {
		log.Printf("route add: %s -> %s", host, entry.TargetURL.String())
	}
	return nil
}

// RemoveRoute removes the mapping for host.
func (m *ShardedRouteManager) RemoveRoute(host string) {
	idx := m.shardIdx(host)
	s := m.shards[idx]
	s.Lock()
	delete(s.m, host)
	s.Unlock()
	if m.logRequests {
		log.Printf("route remove: %s", host)
	}
}

// GetEntry returns the UpstreamEntry for host. This is the hot path for request forwarding.
func (m *ShardedRouteManager) GetEntry(host string) (*UpstreamEntry, bool) {
	idx := m.shardIdx(host)
	s := m.shards[idx]
	s.RLock()
	e, ok := s.m[host]
	s.RUnlock()
	return e, ok
}

// ListRoutes returns a snapshot of host->target for administrative calls.
func (m *ShardedRouteManager) ListRoutes() map[string]string {
	out := make(map[string]string)
	for i := 0; i < routeShards; i++ {
		s := m.shards[i]
		s.RLock()
		for k, v := range s.m {
			out[k] = v.TargetURL.String()
		}
		s.RUnlock()
	}
	return out
}

// FastProxyHandler does:
//  - normalize host (strip port)
//  - single lookup into shard map (GetEntry)
//  - optional header injection (low-cost)
//  - delegate to pre-created ReverseProxy which streams the body
func FastProxyHandler(m *ShardedRouteManager, zone string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Strip optional port from Host (e.g. "alice.example.com:8080")
		host := r.Host
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}

		// Quick reject if host doesn't belong to zone to reduce unnecessary lookups.
		if zone != "" && !strings.HasSuffix(host, "."+zone) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}

		entry, ok := m.GetEntry(host)
		if !ok {
			http.NotFound(w, r)
			return
		}

		// Inject minimal headers for tracing (cheap).
		if m.logRequests {
			if parts := strings.Split(host, "."); len(parts) > 0 {
				r.Header.Set("X-Tunnel-User", parts[0])
			}
		}

		// Serve using pre-created proxy (streams response efficiently).
		entry.Proxy.ServeHTTP(w, r)
	}
}
