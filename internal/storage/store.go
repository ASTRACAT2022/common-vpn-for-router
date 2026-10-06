package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"common-vpn-router/internal/model"
	"common-vpn-router/internal/routing"
)

type State struct {
	Subscriptions        []model.Subscription `json:"subscriptions"`
	ActiveSubscriptionID string               `json:"activeSubscriptionId,omitempty"`
	SelectedNodeID       string               `json:"selectedNodeId,omitempty"`
	RoutingProfiles      []routing.Profile    `json:"routingProfiles,omitempty"`
	ActiveRoutingID      string               `json:"activeRoutingId,omitempty"`
	TrafficMode          routing.TrafficMode  `json:"trafficMode,omitempty"`
	DeviceIPs            []string             `json:"deviceIPs,omitempty"`
	DeviceMACs           []string             `json:"deviceMACs,omitempty"`
	VPNEnabled           bool                 `json:"vpnEnabled,omitempty"`
	AutoMode             bool                 `json:"autoMode,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data State
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secure data directory permissions: %w", err)
	}
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.data = State{Subscriptions: []model.Subscription{}, RoutingProfiles: []routing.Profile{}}
		if err := s.persist(s.data); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure state file permissions: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	if s.data.Subscriptions == nil {
		s.data.Subscriptions = []model.Subscription{}
	}
	if s.data.RoutingProfiles == nil {
		s.data.RoutingProfiles = []routing.Profile{}
	}
	return s, nil
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy, _ := clone(s.data)
	return copy
}

func (s *Store) Update(change func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, err := clone(s.data)
	if err != nil {
		return err
	}
	if err := change(&candidate); err != nil {
		return err
	}
	if candidate.Subscriptions == nil {
		candidate.Subscriptions = []model.Subscription{}
	}
	if candidate.RoutingProfiles == nil {
		candidate.RoutingProfiles = []routing.Profile{}
	}
	if err := s.persist(candidate); err != nil {
		return err
	}
	s.data = candidate
	return nil
}

func (s *Store) persist(data State) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := atomicWrite(s.path, encoded, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".common-vpn-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	return atomicWrite(path, data, mode)
}

func clone(state State) (State, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return State{}, err
	}
	var result State
	if err := json.Unmarshal(b, &result); err != nil {
		return State{}, err
	}
	return result, nil
}
