package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/gofrs/flock"
)

// Store persists server state with in-process and cross-process locking.
type Store struct {
	path string

	mu   sync.Mutex
	lock *flock.Flock
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
		lock: flock.New(path + ".lock"),
	}
}

func (s *Store) Init() error {
	dir := filepath.Dir(s.path)
	if err := fsutil.EnsureDir(dir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(s.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat state file %s: %w", s.path, err)
	}

	state := DefaultServerState()
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal default state: %w", err)
	}
	if err := fsutil.WriteFileAtomic(s.path, body, 0o644); err != nil {
		return fmt.Errorf("write default state: %w", err)
	}
	return nil
}

func (s *Store) Read() (ServerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return ServerState{}, fmt.Errorf("acquire state file lock: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	return s.readUnlocked()
}

func (s *Store) Update(mutator func(*ServerState) error) (ServerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(); err != nil {
		return ServerState{}, fmt.Errorf("acquire state file lock: %w", err)
	}
	defer func() { _ = s.lock.Unlock() }()

	current, err := s.readUnlocked()
	if err != nil {
		return ServerState{}, err
	}

	if err := mutator(&current); err != nil {
		return ServerState{}, err
	}

	payload, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return ServerState{}, fmt.Errorf("marshal state: %w", err)
	}

	if err := fsutil.WriteFileAtomic(s.path, payload, 0o644); err != nil {
		return ServerState{}, fmt.Errorf("write state file: %w", err)
	}

	return current, nil
}

func (s *Store) readUnlocked() (ServerState, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		return ServerState{}, fmt.Errorf("read state file %s: %w", s.path, err)
	}
	if len(content) == 0 {
		return DefaultServerState(), nil
	}

	var st ServerState
	if err := json.Unmarshal(content, &st); err != nil {
		return ServerState{}, fmt.Errorf("decode state file %s: %w", s.path, err)
	}

	if st.Agent.State == "" {
		st.Agent.State = AgentStateEmpty
	}
	if st.Run.State == "" {
		st.Run.State = RunStateIdle
	}

	return st, nil
}
