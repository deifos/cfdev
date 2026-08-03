package inspector

import (
	"net/http"
	"sync"
	"time"
)

const (
	MaxRequests   = 200
	MaxBodyBytes  = 1 << 20
	MaxStoreBytes = 32 << 20
)

type Body struct {
	Bytes      []byte `json:"-"`
	Size       int64  `json:"size"`
	Captured   bool   `json:"captured"`
	Truncated  bool   `json:"truncated"`
	Incomplete bool   `json:"incomplete"`
	Skipped    string `json:"skipped,omitempty"`
}

type Exchange struct {
	ID              uint64      `json:"id"`
	StartedAt       time.Time   `json:"started_at"`
	CompletedAt     time.Time   `json:"completed_at"`
	Method          string      `json:"method"`
	Path            string      `json:"path"`
	Hostname        string      `json:"hostname"`
	Target          string      `json:"target"`
	Status          int         `json:"status"`
	DurationMS      float64     `json:"duration_ms"`
	RequestHeaders  http.Header `json:"request_headers"`
	ResponseHeaders http.Header `json:"response_headers"`
	RequestBody     Body        `json:"request_body"`
	ResponseBody    Body        `json:"response_body"`
	RequestHadBody  bool        `json:"request_had_body"`
	ReplayOf        uint64      `json:"replay_of,omitempty"`
	LocalDown       bool        `json:"local_down"`
	Streaming       bool        `json:"streaming"`
	replayHeaders   http.Header
}

func (exchange Exchange) Replayable() bool {
	return !exchange.Streaming && !exchange.RequestBody.Incomplete && !exchange.ResponseBody.Incomplete && !exchange.ResponseBody.Truncated &&
		(!exchange.RequestHadBody || (exchange.RequestBody.Captured && !exchange.RequestBody.Truncated))
}

type Store struct {
	mu            sync.RWMutex
	nextID        uint64
	bytes         int64
	items         []Exchange
	captureBodies bool
}

func NewStore() *Store { return &Store{nextID: 1} }

func (store *Store) CaptureBodies() bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.captureBodies
}

func (store *Store) SetCaptureBodies(enabled bool) {
	store.mu.Lock()
	store.captureBodies = enabled
	store.mu.Unlock()
}

func (store *Store) Add(exchange Exchange) Exchange {
	store.mu.Lock()
	defer store.mu.Unlock()
	exchange.ID = store.nextID
	store.nextID++
	store.items = append(store.items, exchange)
	store.bytes += bodyMemory(exchange)
	for len(store.items) > MaxRequests || store.bytes > MaxStoreBytes {
		store.bytes -= bodyMemory(store.items[0])
		store.items = store.items[1:]
	}
	return exchange
}

func (store *Store) List() []Exchange {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Exchange, len(store.items))
	for index := range store.items {
		result[len(store.items)-1-index] = store.items[index]
	}
	return result
}

func (store *Store) Get(id uint64) (Exchange, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for index := len(store.items) - 1; index >= 0; index-- {
		if store.items[index].ID == id {
			return store.items[index], true
		}
	}
	return Exchange{}, false
}

func (store *Store) Clear() {
	store.mu.Lock()
	store.items = nil
	store.bytes = 0
	store.mu.Unlock()
}

func (store *Store) MemoryBytes() int64 {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.bytes
}

func bodyMemory(exchange Exchange) int64 {
	return int64(len(exchange.RequestBody.Bytes) + len(exchange.ResponseBody.Bytes))
}
