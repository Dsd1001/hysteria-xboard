package xboard

import (
	"math"
	"sync"

	"github.com/apernet/hysteria/core/v2/server"
)

const trafficShardCount = 32

var _ server.TrafficLogger = (*TrafficCollector)(nil)

// TrafficDelta uses the Xboard accounting perspective: upload is Hysteria Tx
// (server to remote) and download is Hysteria Rx (remote to server).
type TrafficDelta struct {
	Upload   uint64
	Download uint64
}

type trafficShard struct {
	mu     sync.Mutex
	deltas map[string]TrafficDelta
}

// TrafficCollector is the non-blocking data-plane portion of Xboard billing.
// It performs no network or disk I/O.
type TrafficCollector struct {
	store  *UserStore
	shards [trafficShardCount]trafficShard

	onlineMu sync.RWMutex
	online   map[string]int
}

func NewTrafficCollector(store *UserStore) *TrafficCollector {
	collector := &TrafficCollector{
		store:  store,
		online: make(map[string]int),
	}
	for i := range collector.shards {
		collector.shards[i].deltas = make(map[string]TrafficDelta)
	}
	return collector
}

func (c *TrafficCollector) LogTraffic(id string, tx, rx uint64) bool {
	if c == nil || c.store == nil || !c.store.IsActiveID(id) {
		return false
	}
	c.add(id, TrafficDelta{Upload: tx, Download: rx})
	return true
}

func (c *TrafficCollector) add(id string, delta TrafficDelta) {
	shard := &c.shards[trafficShardIndex(id)]
	shard.mu.Lock()
	current := shard.deltas[id]
	current.Upload = saturatingAdd(current.Upload, delta.Upload)
	current.Download = saturatingAdd(current.Download, delta.Download)
	shard.deltas[id] = current
	shard.mu.Unlock()
}

// Drain atomically swaps every shard with an empty map and returns the deltas
// accumulated before each swap.
func (c *TrafficCollector) Drain() map[string]TrafficDelta {
	drained := make(map[string]TrafficDelta)
	if c == nil {
		return drained
	}
	for i := range c.shards {
		shard := &c.shards[i]
		shard.mu.Lock()
		for id, delta := range shard.deltas {
			drained[id] = delta
		}
		shard.deltas = make(map[string]TrafficDelta)
		shard.mu.Unlock()
	}
	return drained
}

// Merge restores previously drained deltas after a persistence failure.
func (c *TrafficCollector) Merge(deltas map[string]TrafficDelta) {
	if c == nil {
		return
	}
	for id, delta := range deltas {
		c.add(id, delta)
	}
}

func (c *TrafficCollector) LogOnlineState(id string, online bool) {
	if c == nil {
		return
	}
	c.onlineMu.Lock()
	defer c.onlineMu.Unlock()
	if online {
		if c.store != nil && c.store.IsActiveID(id) {
			c.online[id]++
		}
		return
	}
	if c.online[id] <= 1 {
		delete(c.online, id)
	} else {
		c.online[id]--
	}
}

func (c *TrafficCollector) OnlineSnapshot() map[string]int {
	result := make(map[string]int)
	if c == nil {
		return result
	}
	c.onlineMu.RLock()
	defer c.onlineMu.RUnlock()
	for id, count := range c.online {
		result[id] = count
	}
	return result
}

func (c *TrafficCollector) TraceStream(server.HyStream, *server.StreamStats) {}

func (c *TrafficCollector) UntraceStream(server.HyStream) {}

func trafficShardIndex(id string) uint32 {
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	hash := offset32
	for i := 0; i < len(id); i++ {
		hash ^= uint32(id[i])
		hash *= prime32
	}
	return hash & (trafficShardCount - 1)
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}
