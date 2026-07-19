package xboard

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	trafficPendingBucket = []byte("pending")
	trafficBatchesBucket = []byte("batches")
	trafficMetaBucket    = []byte("meta")
	trafficSequenceKey   = []byte("sequence")
	trafficNodeIDKey     = []byte("node_id")
)

type TrafficBatch struct {
	ID        string                  `json:"batch_id"`
	NodeID    string                  `json:"node_id"`
	CreatedAt time.Time               `json:"created_at"`
	Traffic   map[string]TrafficDelta `json:"traffic"`
}

type TrafficSpool struct {
	db     *bolt.DB
	nodeID string
}

func OpenTrafficSpool(path, nodeID string) (*TrafficSpool, error) {
	if path == "" {
		return nil, fmt.Errorf("Xboard traffic spool path is required")
	}
	if nodeID == "" {
		return nil, fmt.Errorf("Xboard node ID is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Xboard traffic spool directory: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open Xboard traffic spool: %w", err)
	}
	spool := &TrafficSpool{db: db, nodeID: nodeID}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(trafficPendingBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(trafficBatchesBucket); err != nil {
			return err
		}
		meta, err := tx.CreateBucketIfNotExists(trafficMetaBucket)
		if err != nil {
			return err
		}
		storedNodeID := meta.Get(trafficNodeIDKey)
		if len(storedNodeID) > 0 && string(storedNodeID) != nodeID {
			return fmt.Errorf("traffic spool belongs to Xboard node %q", string(storedNodeID))
		}
		if len(storedNodeID) == 0 {
			return meta.Put(trafficNodeIDKey, []byte(nodeID))
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize Xboard traffic spool: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure Xboard traffic spool: %w", err)
	}
	return spool, nil
}

func (s *TrafficSpool) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *TrafficSpool) AddPending(deltas map[string]TrafficDelta) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("Xboard traffic spool is closed")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(trafficPendingBucket)
		for id, delta := range deltas {
			if id == "" {
				return fmt.Errorf("empty Xboard user ID in traffic delta")
			}
			if delta.Upload == 0 && delta.Download == 0 {
				continue
			}
			current, err := decodeTrafficDelta(bucket.Get([]byte(id)))
			if err != nil {
				return fmt.Errorf("decode pending traffic for user %q: %w", id, err)
			}
			current.Upload = saturatingAdd(current.Upload, delta.Upload)
			current.Download = saturatingAdd(current.Download, delta.Download)
			if err := bucket.Put([]byte(id), encodeTrafficDelta(current)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *TrafficSpool) CreateBatch(createdAt time.Time) (*TrafficBatch, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Xboard traffic spool is closed")
	}
	var result *TrafficBatch
	err := s.db.Update(func(tx *bolt.Tx) error {
		pending := tx.Bucket(trafficPendingBucket)
		if pending.Stats().KeyN == 0 {
			return nil
		}

		traffic := make(map[string]TrafficDelta, pending.Stats().KeyN)
		keys := make([][]byte, 0, pending.Stats().KeyN)
		if err := pending.ForEach(func(key, value []byte) error {
			copiedKey := append([]byte(nil), key...)
			keys = append(keys, copiedKey)
			delta, err := decodeTrafficDelta(value)
			if err != nil {
				return fmt.Errorf("decode pending traffic for user %q: %w", string(key), err)
			}
			traffic[string(key)] = delta
			return nil
		}); err != nil {
			return err
		}

		meta := tx.Bucket(trafficMetaBucket)
		sequence := decodeUint64(meta.Get(trafficSequenceKey)) + 1
		if err := meta.Put(trafficSequenceKey, encodeUint64(sequence)); err != nil {
			return err
		}
		batchID := fmt.Sprintf("hy-%020d-%019d", sequence, createdAt.UnixNano())
		batch := &TrafficBatch{
			ID:        batchID,
			NodeID:    s.nodeID,
			CreatedAt: createdAt.UTC(),
			Traffic:   traffic,
		}
		encoded, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		if err := tx.Bucket(trafficBatchesBucket).Put([]byte(batchID), encoded); err != nil {
			return err
		}
		for _, key := range keys {
			if err := pending.Delete(key); err != nil {
				return err
			}
		}
		result = batch
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create Xboard traffic batch: %w", err)
	}
	return result, nil
}

func (s *TrafficSpool) OldestBatch() (*TrafficBatch, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("Xboard traffic spool is closed")
	}
	var result *TrafficBatch
	err := s.db.View(func(tx *bolt.Tx) error {
		_, value := tx.Bucket(trafficBatchesBucket).Cursor().First()
		if value == nil {
			return nil
		}
		var batch TrafficBatch
		if err := json.Unmarshal(value, &batch); err != nil {
			return err
		}
		result = &batch
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read oldest Xboard traffic batch: %w", err)
	}
	return result, nil
}

func (s *TrafficSpool) AckBatch(batchID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("Xboard traffic spool is closed")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(trafficBatchesBucket).Delete([]byte(batchID))
	})
}

func (s *TrafficSpool) BatchCount() (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("Xboard traffic spool is closed")
	}
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		count = tx.Bucket(trafficBatchesBucket).Stats().KeyN
		return nil
	})
	return count, err
}

func encodeTrafficDelta(delta TrafficDelta) []byte {
	encoded := make([]byte, 16)
	binary.BigEndian.PutUint64(encoded[:8], delta.Upload)
	binary.BigEndian.PutUint64(encoded[8:], delta.Download)
	return encoded
}

func decodeTrafficDelta(encoded []byte) (TrafficDelta, error) {
	if len(encoded) == 0 {
		return TrafficDelta{}, nil
	}
	if len(encoded) != 16 {
		return TrafficDelta{}, fmt.Errorf("invalid encoded traffic length: %d", len(encoded))
	}
	return TrafficDelta{
		Upload:   binary.BigEndian.Uint64(encoded[:8]),
		Download: binary.BigEndian.Uint64(encoded[8:]),
	}, nil
}

func encodeUint64(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

func decodeUint64(encoded []byte) uint64 {
	if len(encoded) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(encoded)
}
