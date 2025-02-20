// Copyright JAMF Software, LLC

package kv

import (
	"cmp"
	"encoding/json"
	"path"
	"strings"
	"sync"

	"golang.org/x/exp/slices"
)

// A MapStore represents an in-memory key-value store safe for
// concurrent access.
type MapStore struct {
	mtx sync.RWMutex
	m   map[string]Pair
}

// NewMapStore creates and initializes a new MapStore.
func NewMapStore() *MapStore {
	return &MapStore{m: make(map[string]Pair)}
}

// Delete deletes the Pair associated with key.
func (s *MapStore) Delete(key string, ver uint64) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	delete(s.m, key)
	return nil
}

// Exists checks for the existence of key in the store.
func (s *MapStore) Exists(key string) (bool, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	_, ok := s.m[key]
	return ok, nil
}

// Get gets the Pair associated with key. If there is no Pair
// associated with key, Get returns Pair{}, ErrNotExist.
func (s *MapStore) Get(key string) (Pair, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	pair, ok := s.m[key]
	if !ok {
		return Pair{}, ErrNotExist
	}
	return pair, nil
}

// GetAll returns a Pair for all nodes with keys matching pattern.
// The syntax of patterns is the same as in path.Match.
func (s *MapStore) GetAll(pattern string) ([]Pair, error) {
	ks := make([]Pair, 0)
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	for _, kv := range s.m {
		m, err := path.Match(pattern, kv.Key)
		if err != nil {
			return nil, err
		}
		if m {
			ks = append(ks, kv)
		}
	}
	if len(ks) == 0 {
		return ks, nil
	}
	slices.SortFunc(ks, func(a, b Pair) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return ks, nil
}

// GetAllValues returns a []string for all nodes with keys matching pattern.
// The syntax of patterns is the same as in path.Match.
func (s *MapStore) GetAllValues(pattern string) ([]string, error) {
	vs := make([]string, 0)
	ks, err := s.GetAll(pattern)
	if err != nil {
		return vs, err
	}
	if len(ks) == 0 {
		return vs, nil
	}
	for _, kv := range ks {
		vs = append(vs, kv.Value)
	}
	slices.Sort(vs)
	return vs, nil
}

func (s *MapStore) List(filePath string) ([]string, error) {
	vs := make([]string, 0)
	m := make(map[string]bool)
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	prefix := pathToTerms(filePath)
	for _, kv := range s.m {
		if kv.Key == filePath {
			m[path.Base(kv.Key)] = true
			continue
		}
		target := pathToTerms(path.Dir(kv.Key))
		if samePrefixTerms(prefix, target) {
			m[strings.Split(stripKey(kv.Key, filePath), "/")[0]] = true
		}
	}
	for k := range m {
		vs = append(vs, k)
	}
	slices.Sort(vs)
	return vs, nil
}

func (s *MapStore) ListDir(filePath string) ([]string, error) {
	vs := make([]string, 0)
	m := make(map[string]bool)
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	prefix := pathToTerms(filePath)
	for _, kv := range s.m {
		if strings.HasPrefix(kv.Key, filePath) {
			items := pathToTerms(path.Dir(kv.Key))
			if samePrefixTerms(prefix, items) && (len(items)-len(prefix) >= 1) {
				m[items[len(prefix):][0]] = true
			}
		}
	}
	for k := range m {
		vs = append(vs, k)
	}
	slices.Sort(vs)
	return vs, nil
}

// Set sets the Pair entry associated with key to value.
func (s *MapStore) Set(key string, value string, ver uint64) (Pair, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	p := Pair{Key: key, Value: value, Ver: ver}
	if s.m == nil {
		s.m = make(map[string]Pair)
	}
	s.m[key] = p
	return p, nil
}

func (s *MapStore) MarshalJSON() ([]byte, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return json.Marshal(s.m)
}

func (s *MapStore) UnmarshalJSON(bytes []byte) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.m = make(map[string]Pair)
	return json.Unmarshal(bytes, &s.m)
}
