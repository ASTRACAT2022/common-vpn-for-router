package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"common-vpn-router/internal/node"
	"common-vpn-router/internal/storage"
)

const maxSubscriptionSize = 2 << 20
const defaultUpdateIntervalHours = 24
const maxUpdateIntervalHours = 168

type Manager struct {
	mu     sync.Mutex
	store  *storage.Store
	client *http.Client
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{store: store, client: &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 || len(via) >= 5 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
				return errors.New("subscription redirect is not allowed")
			}
			if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("subscription redirect cannot downgrade HTTPS")
			}
			return nil
		},
	}}
}

func (m *Manager) Add(ctx context.Context, rawURL, name string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parsedURL, err := validateSubscriptionURL(rawURL)
	if err != nil {
		return Subscription{}, err
	}
	body, etag, modified, interval, err := m.download(ctx, parsedURL.String(), "", "")
	if err != nil {
		return Subscription{}, err
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		return Subscription{}, fmt.Errorf("parse subscription: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		name = parsedURL.Hostname()
	}
	id, err := randomID()
	if err != nil {
		return Subscription{}, err
	}
	parsed.Nodes = scopeNodeIDs(id, parsed.Nodes)
	if interval == 0 {
		interval = defaultUpdateIntervalHours
	}
	sub := Subscription{ID: id, Name: name, URL: parsedURL.String(), Nodes: parsed.Nodes, UpdatedAt: time.Now().UTC(), UpdateIntervalHours: interval, ETag: etag, LastModified: modified}
	err = m.store.Update(func(state *storage.State) error {
		state.Subscriptions = append(state.Subscriptions, sub)
		if state.ActiveSubscriptionID == "" {
			state.ActiveSubscriptionID = id
		}
		return nil
	})
	return sub, err
}

func (m *Manager) Update(ctx context.Context, id, rawURL, name string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, id, rawURL, name, false)
}

// ForceRefresh downloads the full body even when the provider sent validators
// on an earlier response. The stored subscription is replaced only after parsing
// the new body succeeds.
func (m *Manager) ForceRefresh(ctx context.Context, id string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(ctx, id, "", "", true)
}

func (m *Manager) update(ctx context.Context, id, rawURL, name string, force bool) (Subscription, error) {
	current, ok := m.find(id)
	if !ok {
		return Subscription{}, errors.New("subscription not found")
	}
	if rawURL == "" {
		rawURL = current.URL
	}
	parsedURL, err := validateSubscriptionURL(rawURL)
	if err != nil {
		return Subscription{}, err
	}
	conditionalETag, conditionalModified := current.ETag, current.LastModified
	if force || parsedURL.String() != current.URL {
		conditionalETag, conditionalModified = "", ""
	}
	body, etag, modified, interval, err := m.download(ctx, parsedURL.String(), conditionalETag, conditionalModified)
	if err != nil {
		return Subscription{}, err
	}
	if body == nil { // HTTP 304: the stored, known-good version remains current.
		current.UpdatedAt = time.Now().UTC()
		if interval > 0 {
			current.UpdateIntervalHours = interval
		}
		if etag != "" {
			current.ETag = etag
		}
		if modified != "" {
			current.LastModified = modified
		}
		err = m.store.Update(func(state *storage.State) error {
			for i := range state.Subscriptions {
				if state.Subscriptions[i].ID == id {
					state.Subscriptions[i] = current
					return nil
				}
			}
			return errors.New("subscription not found")
		})
		return current, err
	}
	parsed, err := ParsePayload(body)
	if err != nil {
		return Subscription{}, fmt.Errorf("parse subscription: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		name = current.Name
	}
	parsed.Nodes = scopeNodeIDs(id, parsed.Nodes)
	updated := current
	updated.Name, updated.URL, updated.Nodes = name, parsedURL.String(), parsed.Nodes
	updated.UpdatedAt, updated.ETag, updated.LastModified = time.Now().UTC(), etag, modified
	updated.UpdateIntervalHours = defaultUpdateIntervalHours
	if interval > 0 {
		updated.UpdateIntervalHours = interval
	}
	err = m.store.Update(func(state *storage.State) error {
		for i := range state.Subscriptions {
			if state.Subscriptions[i].ID == id {
				state.Subscriptions[i] = updated
				return nil
			}
		}
		return errors.New("subscription not found")
	})
	return updated, err
}

func (m *Manager) Refresh(ctx context.Context, id string) (Subscription, error) {
	return m.Update(ctx, id, "", "")
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.Update(func(state *storage.State) error {
		for i := range state.Subscriptions {
			if state.Subscriptions[i].ID == id {
				state.Subscriptions = append(state.Subscriptions[:i], state.Subscriptions[i+1:]...)
				if state.ActiveSubscriptionID == id {
					state.ActiveSubscriptionID = ""
					state.SelectedNodeID = ""
				}
				return nil
			}
		}
		return errors.New("subscription not found")
	})
}

func (m *Manager) find(id string) (Subscription, bool) {
	for _, sub := range m.store.Snapshot().Subscriptions {
		if sub.ID == id {
			return sub, true
		}
	}
	return Subscription{}, false
}

func (m *Manager) download(ctx context.Context, target, etag, modified string) ([]byte, string, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("create subscription request: %w", err)
	}
	req.Header.Set("User-Agent", "Common-VPN-Router/0.1")
	req.Header.Set("Accept", "text/plain, application/json, */*")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, "", "", 0, errors.New("subscription download failed")
	}
	defer resp.Body.Close()
	interval := parseUpdateInterval(resp.Header.Get("Profile-Update-Interval"))
	if resp.StatusCode == http.StatusNotModified {
		return nil, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), interval, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", 0, fmt.Errorf("subscription server returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionSize+1))
	if err != nil {
		return nil, "", "", 0, errors.New("subscription response could not be read")
	}
	if len(body) > maxSubscriptionSize {
		return nil, "", "", 0, errors.New("subscription exceeds 2 MiB limit")
	}
	return body, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), interval, nil
}

func parseUpdateInterval(value string) int {
	hours, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || hours < 1 {
		return 0
	}
	if hours > maxUpdateIntervalHours {
		return maxUpdateIntervalHours
	}
	return hours
}

func validateSubscriptionURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("subscription URL is invalid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("subscription URL must use http or https")
	}
	if u.User != nil {
		return nil, errors.New("subscription URL must not contain embedded credentials")
	}
	return u, nil
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func scopeNodeIDs(subscriptionID string, nodes []node.Node) []node.Node {
	result := make([]node.Node, len(nodes))
	copy(result, nodes)
	for i := range result {
		result[i].ID = node.StableID(subscriptionID + ":" + result[i].ID)
	}
	return result
}
