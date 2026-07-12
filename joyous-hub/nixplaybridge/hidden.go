package nixplaybridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// HiddenStore persists the set of Nixplay gallery ids the user has chosen to
// hide from the hub's Devices/Send lists — a preference, not an account
// change (the galleries still exist in Nixplay, they just aren't offered as
// a Send target on this hub).
type HiddenStore struct {
	path string

	mu     sync.Mutex
	hidden map[string]bool
}

func NewHiddenStore(dataDir string) *HiddenStore {
	return &HiddenStore{
		path:   filepath.Join(dataDir, "hidden_galleries.json"),
		hidden: map[string]bool{},
	}
}

// Load reads the persisted hidden set. A missing file is not an error (fresh install).
func (s *HiddenStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		s.hidden[id] = true
	}
	return nil
}

func (s *HiddenStore) IsHidden(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hidden[id]
}

// SetHidden updates the hidden set and persists it immediately.
func (s *HiddenStore) SetHidden(id string, hidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hidden {
		s.hidden[id] = true
	} else {
		delete(s.hidden, id)
	}
	ids := make([]string, 0, len(s.hidden))
	for id := range s.hidden {
		ids = append(ids, id)
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
