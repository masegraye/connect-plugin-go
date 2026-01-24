package kvimpl

import (
	"context"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	kvv1 "github.com/example/connect-plugin-go/examples/kv/gen"
)

// Store is a simple in-memory key-value store implementation.
type Store struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewStore creates a new in-memory KV store.
func NewStore() *Store {
	return &Store{
		data: make(map[string][]byte),
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

	return connect.NewResponse(&kvv1.PutResponse{}), nil
}

// Delete removes a key.
func (s *Store) Delete(ctx context.Context, req *connect.Request[kvv1.DeleteRequest]) (*connect.Response[kvv1.DeleteResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, found := s.data[req.Msg.Key]
	delete(s.data, req.Msg.Key)

	return connect.NewResponse(&kvv1.DeleteResponse{
		Found: found,
	}), nil
}
