package store

// Store is the keyspace owned by the executor goroutine. It has no locks:
// ADR-002 makes that goroutine the only accessor.
type Store struct {
	data map[string][]byte
}

func New() *Store {
	return &Store{data: make(map[string][]byte)}
}

// Get returns the stored bytes. The slice is owned by the store and must not
// be mutated by callers.
func (s *Store) Get(key string) ([]byte, bool) {
	value, ok := s.data[key]
	return value, ok
}

// Set stores value under key. The value slice is retained as-is.
func (s *Store) Set(key string, value []byte) {
	s.data[key] = value
}

func (s *Store) Delete(key string) bool {
	_, ok := s.data[key]
	delete(s.data, key)
	return ok
}

func (s *Store) Exists(key string) bool {
	_, ok := s.data[key]
	return ok
}

func (s *Store) Len() int {
	return len(s.data)
}
