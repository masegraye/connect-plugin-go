package kvimpl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"connectrpc.com/connect"
	connectplugin "github.com/example/connect-plugin-go"
	kvv1 "github.com/example/connect-plugin-go/examples/kv/gen"
	"github.com/example/connect-plugin-go/examples/kv/gen/kvv1connect"
)

// Compile-time check that Store implements KVServiceHandler
var _ kvv1connect.KVServiceHandler = (*Store)(nil)

// Store is a simple in-memory key-value store implementation.
type Store struct {
	mu       sync.RWMutex
	data     map[string][]byte
	watchers []*watcher
}

type watcher struct {
	prefix string
	ch     chan kvv1.WatchEvent
	ctx    context.Context
}

// NewStore creates a new in-memory KV store.
func NewStore() *Store {
	return &Store{
		data:     make(map[string][]byte),
		watchers: make([]*watcher, 0),
	}
}

// Get retrieves a value by key.
func (s *Store) Get(ctx context.Context, req *connect.Request[kvv1.GetRequest]) (*connect.Response[kvv1.GetResponse], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, found := s.data[req.Msg.Key]

	return connect.NewResponse(&kvv1.GetResponse{
		Value: value,
		Found: found,
	}), nil
}

// Put stores a key-value pair.
func (s *Store) Put(ctx context.Context, req *connect.Request[kvv1.PutRequest]) (*connect.Response[kvv1.PutResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Msg.Key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("key cannot be empty"))
	}

	s.data[req.Msg.Key] = req.Msg.Value

	// Notify watchers
	s.notifyWatchers(kvv1.WatchEvent{
		Type:  kvv1.EventType_EVENT_TYPE_PUT,
		Key:   req.Msg.Key,
		Value: req.Msg.Value,
	})

	return connect.NewResponse(&kvv1.PutResponse{}), nil
}

// Delete removes a key.
func (s *Store) Delete(ctx context.Context, req *connect.Request[kvv1.DeleteRequest]) (*connect.Response[kvv1.DeleteResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, found := s.data[req.Msg.Key]
	delete(s.data, req.Msg.Key)

	// Notify watchers
	if found {
		s.notifyWatchers(kvv1.WatchEvent{
			Type: kvv1.EventType_EVENT_TYPE_DELETE,
			Key:  req.Msg.Key,
		})
	}

	return connect.NewResponse(&kvv1.DeleteResponse{
		Found: found,
	}), nil
}

// Watch streams changes to keys with the given prefix.
// Uses PumpToStream adapter for channel-based streaming (design-uxvj).
func (s *Store) Watch(ctx context.Context, req *connect.Request[kvv1.WatchRequest], stream *connect.ServerStream[kvv1.WatchEvent]) error {
	events := make(chan kvv1.WatchEvent, 32)
	errs := make(chan error, 1)

	// Register watcher
	w := &watcher{
		prefix: req.Msg.Prefix,
		ch:     events,
		ctx:    ctx,
	}

	s.mu.Lock()
	s.watchers = append(s.watchers, w)
	s.mu.Unlock()

	// Send initial event to establish stream (Connect requires this for headers)
	// Client should filter this out as it's not a real data event
	events <- kvv1.WatchEvent{
		Type: kvv1.EventType_EVENT_TYPE_UNSPECIFIED,
		Key:  "_watch_started",
	}

	// Cleanup on exit
	defer func() {
		s.mu.Lock()
		for i, watcher := range s.watchers {
			if watcher == w {
				s.watchers = append(s.watchers[:i], s.watchers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(events)
	}()

	// Stream events using adapter
	return connectplugin.PumpToStream(ctx, events, errs, stream)
}

// notifyWatchers sends an event to all matching watchers.
// Caller must hold lock.
func (s *Store) notifyWatchers(event kvv1.WatchEvent) {
	for _, w := range s.watchers {
		// Check if event matches watcher's prefix
		if w.prefix == "" || strings.HasPrefix(event.Key, w.prefix) {
			select {
			case w.ch <- event:
			case <-w.ctx.Done():
				// Watcher's context cancelled, skip
			default:
				// Watcher slow, skip (non-blocking)
			}
		}
	}
}
