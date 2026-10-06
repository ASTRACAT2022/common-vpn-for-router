package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"common-vpn-router/internal/config"
	"common-vpn-router/internal/devices"
	"common-vpn-router/internal/health"
	"common-vpn-router/internal/netroute"
	"common-vpn-router/internal/node"
	"common-vpn-router/internal/routing"
	"common-vpn-router/internal/storage"
	"common-vpn-router/internal/subscription"
	"common-vpn-router/internal/webui"
	"common-vpn-router/internal/xray"
)

type Server struct {
	config        config.Config
	store         *storage.Store
	subscriptions *subscription.Manager
	xray          *xray.Controller
	logger        *slog.Logger
	operationMu   sync.Mutex
	healthMu      sync.Mutex
	healthPID     int
	healthOK      bool
	healthAt      time.Time
	activeSources []string
	deviceMonitor devices.Monitor
}

func NewServer(cfg config.Config, store *storage.Store, subs *subscription.Manager, controller *xray.Controller, logger *slog.Logger) *Server {
	return &Server{config: cfg, store: store, subscriptions: subs, xray: controller, logger: logger}
}

// RestoreVPN regenerates the persisted Xray config with the current WAN
// interface and routing settings before restarting an enabled tunnel.
func (s *Server) RestoreVPN(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	state := s.store.Snapshot()
	if !state.VPNEnabled {
		return nil
	}
	selected, ok := selectedNode(state)
	if !ok {
		return errors.New("VPN is enabled but there is no selected server to restore")
	}
	return s.applyNode(ctx, state, selected, state.AutoMode)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	uiFiles := http.FileServer(http.FS(webui.Files()))
	mux.Handle("GET /{$}", uiFiles)
	mux.Handle("GET /app.css", uiFiles)
	mux.Handle("GET /app.js", uiFiles)
	mux.Handle("GET /commonnetwork-mark.png", uiFiles)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/subscriptions", s.listSubscriptions)
	mux.HandleFunc("POST /api/subscriptions", s.addSubscription)
	mux.HandleFunc("GET /api/subscriptions/{id}", s.getSubscription)
	mux.HandleFunc("PUT /api/subscriptions/{id}", s.updateSubscription)
	mux.HandleFunc("DELETE /api/subscriptions/{id}", s.deleteSubscription)
	mux.HandleFunc("POST /api/subscriptions/{id}/update", s.refreshSubscription)
	mux.HandleFunc("GET /api/nodes", s.listNodes)
	mux.HandleFunc("POST /api/nodes/{id}/select", s.selectNode)
	mux.HandleFunc("GET /api/routing", s.getRouting)
	mux.HandleFunc("PUT /api/traffic", s.updateTraffic)
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("GET /api/device-traffic", s.deviceTraffic)
	mux.HandleFunc("POST /api/routing/import", s.importRouting)
	mux.HandleFunc("DELETE /api/routing/{id}", s.deleteRouting)
	mux.HandleFunc("POST /api/vpn/connect", s.connect)
	mux.HandleFunc("POST /api/vpn/auto-connect", s.autoConnect)
	mux.HandleFunc("POST /api/vpn/auto-disable", s.disableAuto)
	mux.HandleFunc("POST /api/vpn/disconnect", s.disconnect)
	mux.HandleFunc("POST /api/vpn/restart", s.restart)
	mux.HandleFunc("GET /api/system/info", s.systemInfo)
	return securityHeaders(requestGuard(requestLog(s.logger, mux)))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	state := s.store.Snapshot()
	profile, hasProfile := findRoutingProfile(state, state.ActiveRoutingID)
	status := s.xray.Status()
	s.healthMu.Lock()
	healthy := status.Running && s.healthPID == status.PID && s.healthOK
	checkedAt := s.healthAt
	s.healthMu.Unlock()
	response := map[string]any{"connected": healthy, "transportActive": status.Running, "healthCheckedAt": checkedAt, "vpnEnabled": state.VPNEnabled, "xray": status, "version": s.config.Version,
		"selectedNode": nil, "subscription": nil, "tunnel": s.config.Tunnel, "routing": nil, "autoMode": state.AutoMode,
		"trafficMode": routing.EffectiveTrafficMode(state.TrafficMode, hasProfile), "deviceIPs": state.DeviceIPs, "deviceMACs": state.DeviceMACs}
	if hasProfile {
		response["routing"] = map[string]any{"id": profile.ID, "name": profile.Name, "globalProxy": profile.GlobalProxy}
	}
	for _, sub := range state.Subscriptions {
		if sub.ID == state.ActiveSubscriptionID {
			response["subscription"] = map[string]any{"id": sub.ID, "name": sub.Name, "nodeCount": len(sub.Nodes), "updatedAt": sub.UpdatedAt, "updateIntervalHours": subscriptionIntervalHours(sub)}
		}
		for _, n := range sub.Nodes {
			if n.ID == state.SelectedNodeID {
				response["selectedNode"] = summarizeNode(n)
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) listSubscriptions(w http.ResponseWriter, _ *http.Request) {
	state := s.store.Snapshot()
	result := make([]subscriptionSummary, 0, len(state.Subscriptions))
	for _, sub := range state.Subscriptions {
		result = append(result, summarizeSubscription(sub))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) addSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	sub, err := s.subscriptions.Add(r.Context(), input.URL, input.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, summarizeSubscription(sub))
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	sub, ok := findSubscription(s.store.Snapshot(), r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "subscription not found")
		return
	}
	writeJSON(w, http.StatusOK, summarizeSubscription(sub))
}

func (s *Server) updateSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	sub, err := s.subscriptions.Update(r.Context(), r.PathValue("id"), input.URL, input.Name)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "subscription not found" {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summarizeSubscription(sub))
}

func (s *Server) refreshSubscription(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	sub, err := s.refreshSubscriptionData(r.Context(), r.PathValue("id"), true)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "subscription not found" {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summarizeSubscription(sub))
}

func (s *Server) refreshSubscriptionData(ctx context.Context, id string, force bool) (subscription.Subscription, error) {
	var updated subscription.Subscription
	var err error
	if force {
		updated, err = s.subscriptions.ForceRefresh(ctx, id)
	} else {
		updated, err = s.subscriptions.Refresh(ctx, id)
	}
	if err != nil {
		return subscription.Subscription{}, err
	}
	state := s.store.Snapshot()
	if state.ActiveSubscriptionID != id {
		return updated, nil
	}
	if len(updated.Nodes) == 0 {
		return updated, errors.New("subscription contains no servers")
	}
	replacement := updated.Nodes[0]
	for _, n := range updated.Nodes {
		if n.ID == state.SelectedNodeID {
			replacement = n
			break
		}
	}
	if state.AutoMode && state.VPNEnabled {
		candidates := s.probeCandidates(ctx, state, "")
		if len(candidates) > 0 {
			replacement = candidates[0].node
		}
	}
	if state.VPNEnabled {
		if err := s.applyNode(ctx, state, replacement, state.AutoMode); err != nil {
			return updated, fmt.Errorf("subscription updated, but the replacement server could not be applied: %w", err)
		}
		return updated, nil
	}
	if err := s.store.Update(func(current *storage.State) error {
		current.ActiveSubscriptionID = id
		current.SelectedNodeID = replacement.ID
		return nil
	}); err != nil {
		return updated, errors.New("subscription updated, but the selected server could not be saved")
	}
	return updated, nil
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	id := r.PathValue("id")
	state := s.store.Snapshot()
	if state.ActiveSubscriptionID == id && s.xray.Status().Running {
		if err := s.xray.Stop(); err != nil {
			writeError(w, http.StatusInternalServerError, "could not stop Xray before deleting active subscription")
			return
		}
	}
	if err := s.subscriptions.Delete(id); err != nil {
		status := http.StatusBadRequest
		if err.Error() == "subscription not found" {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	if state.ActiveSubscriptionID == id {
		if err := s.store.Update(func(state *storage.State) error { state.VPNEnabled = false; state.AutoMode = false; return nil }); err != nil {
			writeError(w, http.StatusInternalServerError, "subscription was deleted but VPN state could not be saved")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNodes(w http.ResponseWriter, _ *http.Request) {
	state := s.store.Snapshot()
	nodes := make([]nodeSummary, 0)
	for _, sub := range state.Subscriptions {
		for _, n := range sub.Nodes {
			summary := summarizeNode(n)
			summary.SubscriptionID = sub.ID
			summary.SubscriptionName = sub.Name
			summary.Selected = n.ID == state.SelectedNodeID
			nodes = append(nodes, summary)
		}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) selectNode(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	id := r.PathValue("id")
	state := s.store.Snapshot()
	var selectedSubscription string
	found := false
	for _, sub := range state.Subscriptions {
		for _, n := range sub.Nodes {
			if n.ID == id {
				selectedSubscription, found = sub.ID, true
				if state.VPNEnabled {
					if err := s.applyNode(r.Context(), state, n, false); err != nil {
						writeError(w, http.StatusBadGateway, err.Error())
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				break
			}
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	if err := s.store.Update(func(state *storage.State) error {
		state.ActiveSubscriptionID, state.SelectedNodeID = selectedSubscription, id
		state.AutoMode = false
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save selected node")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRouting(w http.ResponseWriter, _ *http.Request) {
	state := s.store.Snapshot()
	profile, ok := findRoutingProfile(state, state.ActiveRoutingID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"profile": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

func (s *Server) updateTraffic(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Mode       routing.TrafficMode `json:"mode"`
		DeviceIPs  []string            `json:"deviceIPs"`
		DeviceMACs []string            `json:"deviceMACs"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	previous := s.store.Snapshot()
	_, hasProfile := findRoutingProfile(previous, previous.ActiveRoutingID)
	cleanIPs, err := routing.ValidateTraffic(input.Mode, input.DeviceIPs, hasProfile)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(input.DeviceMACs) > 64 {
		writeError(w, http.StatusBadRequest, "no more than 64 device MAC addresses are allowed")
		return
	}
	macs := make([]string, 0, len(input.DeviceMACs))
	seenMACs := map[string]bool{}
	for _, value := range input.DeviceMACs {
		mac, ok := devices.NormalizeMAC(value)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid device MAC address: "+value)
			return
		}
		if !seenMACs[mac] {
			seenMACs[mac] = true
			macs = append(macs, mac)
		}
	}
	if err := s.store.Update(func(state *storage.State) error {
		state.TrafficMode = input.Mode
		state.DeviceIPs = cleanIPs
		state.DeviceMACs = macs
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save traffic settings")
		return
	}
	applied := false
	if s.xray.Status().Running {
		state := s.store.Snapshot()
		selected, ok := selectedNode(state)
		if !ok {
			s.restoreTrafficState(previous)
			writeError(w, http.StatusConflict, "traffic settings were not applied because no server is selected")
			return
		}
		if err := s.applyNode(r.Context(), state, selected, state.AutoMode); err != nil {
			s.restoreTrafficState(previous)
			writeError(w, http.StatusBadGateway, "traffic settings were not applied: "+err.Error())
			return
		}
		applied = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": input.Mode, "deviceIPs": cleanIPs, "deviceMACs": macs, "applied": applied})
}

func (s *Server) listDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, devices.Discover())
}

func (s *Server) deviceTraffic(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.deviceMonitor.Snapshot(devices.Discover())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error(), "devices": []devices.Traffic{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "devices": rows})
}

func (s *Server) restoreTrafficState(previous storage.State) {
	_ = s.store.Update(func(state *storage.State) error {
		state.TrafficMode = previous.TrafficMode
		state.DeviceIPs = previous.DeviceIPs
		state.DeviceMACs = previous.DeviceMACs
		return nil
	})
}

func (s *Server) importRouting(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Link string `json:"link"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	profile, err := routing.Import(input.Link)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	previous := s.store.Snapshot()
	if err := s.store.Update(func(state *storage.State) error {
		state.RoutingProfiles = []routing.Profile{profile}
		state.ActiveRoutingID = profile.ID
		state.TrafficMode = routing.TrafficHapp
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save routing profile")
		return
	}
	applied := false
	if s.xray.Status().Running {
		state := s.store.Snapshot()
		selected, ok := selectedNode(state)
		if !ok {
			s.restoreRoutingState(previous)
			writeError(w, http.StatusConflict, "routing profile was not applied because no server is selected")
			return
		}
		if err := s.applyNode(r.Context(), state, selected, state.AutoMode); err != nil {
			s.restoreRoutingState(previous)
			writeError(w, http.StatusBadGateway, "routing profile was saved but could not be applied: "+err.Error())
			return
		}
		applied = true
	}
	writeJSON(w, http.StatusCreated, map[string]any{"profile": profile, "applied": applied})
}

func (s *Server) deleteRouting(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	id := r.PathValue("id")
	previous := s.store.Snapshot()
	found := false
	err := s.store.Update(func(state *storage.State) error {
		profiles := make([]routing.Profile, 0, len(state.RoutingProfiles))
		for _, profile := range state.RoutingProfiles {
			if profile.ID == id {
				found = true
				continue
			}
			profiles = append(profiles, profile)
		}
		if found {
			state.RoutingProfiles = profiles
			if state.ActiveRoutingID == id {
				state.ActiveRoutingID = ""
				state.TrafficMode = routing.TrafficAll
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove routing profile")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "routing profile not found")
		return
	}
	if s.xray.Status().Running {
		state := s.store.Snapshot()
		selected, ok := selectedNode(state)
		if !ok {
			s.restoreRoutingState(previous)
			writeError(w, http.StatusConflict, "routing profile was not removed because no server is selected")
			return
		}
		if err := s.applyNode(r.Context(), state, selected, state.AutoMode); err != nil {
			s.restoreRoutingState(previous)
			writeError(w, http.StatusBadGateway, "routing profile was removed but the VPN could not be updated: "+err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreRoutingState(previous storage.State) {
	_ = s.store.Update(func(state *storage.State) error {
		state.RoutingProfiles = previous.RoutingProfiles
		state.ActiveRoutingID = previous.ActiveRoutingID
		state.TrafficMode = previous.TrafficMode
		return nil
	})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	state := s.store.Snapshot()
	selected, ok := selectedNode(state)
	if !ok {
		writeError(w, http.StatusConflict, "select a server before connecting")
		return
	}
	if err := s.applyNode(r.Context(), state, selected, false); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": s.xray.Status().Running, "selectedNode": summarizeNode(selected)})
}

func (s *Server) autoConnect(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	state := s.store.Snapshot()
	if countNodes(state) == 0 {
		writeError(w, http.StatusConflict, "add a subscription with servers before using auto mode")
		return
	}
	candidates := s.probeCandidates(r.Context(), state, "")
	if len(candidates) == 0 {
		if err := s.store.Update(func(state *storage.State) error {
			state.AutoMode = true
			state.VPNEnabled = true
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save automatic mode")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connected": s.xray.Status().Running, "autoMode": true, "pending": true})
		return
	}
	best := candidates[0]
	if err := s.applyNode(r.Context(), state, best.node, true); err != nil {
		writeError(w, http.StatusBadGateway, "best server passed its test but VPN could not start")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": s.xray.Status().Running, "autoMode": true, "selectedNode": summarizeNode(best.node), "latencyMs": best.latency.Milliseconds()})
}

func (s *Server) disableAuto(w http.ResponseWriter, _ *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if err := s.store.Update(func(state *storage.State) error {
		state.AutoMode = false
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not stop automatic monitoring")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": s.xray.Status().Running, "autoMode": false})
}

func (s *Server) applyNode(ctx context.Context, state storage.State, selected node.Node, auto bool) error {
	var profile *routing.Profile
	value, hasProfile := findRoutingProfile(state, state.ActiveRoutingID)
	mode := routing.EffectiveTrafficMode(state.TrafficMode, hasProfile)
	if hasProfile && mode == routing.TrafficHapp {
		profile = &value
	}
	assetDir := ""
	if profile != nil && (profile.GeoIPURL != "" || profile.GeoSiteURL != "") {
		standard := os.Getenv("XRAY_LOCATION_ASSET")
		if standard == "" {
			standard = filepath.Dir(s.config.XrayBinary)
		}
		var err error
		assetDir, err = routing.EnsureAssets(ctx, *profile, filepath.Join(s.config.ConfigDir, "routing-assets"), standard)
		if err != nil {
			return fmt.Errorf("prepare routing geo files: %w", err)
		}
	}
	uplink := ""
	if s.config.Tunnel {
		var err error
		uplink, err = netroute.DefaultOutboundInterface()
		if err != nil {
			return fmt.Errorf("detect physical WAN interface for VPN tunnel: %w", err)
		}
	}
	deviceIPs := append([]string(nil), state.DeviceIPs...)
	deviceIPs = append(deviceIPs, devices.Resolve(state.DeviceMACs, devices.Discover())...)
	sort.Strings(deviceIPs)
	content, err := xray.GenerateWithOptions(selected, xray.Options{Tunnel: s.config.Tunnel, RoutingProfile: profile, TrafficMode: mode, DeviceIPs: deviceIPs, RestrictDevices: len(state.DeviceMACs) > 0, DisableIPv6: netroute.IPv6Disabled(), OutboundInterface: uplink})
	if err != nil {
		return err
	}
	if err := s.xray.ApplyWithAssets(ctx, content, assetDir); err != nil {
		return err
	}
	s.activeSources = slices.Clone(deviceIPs)
	if err := s.store.Update(func(state *storage.State) error {
		state.SelectedNodeID = selected.ID
		state.VPNEnabled = true
		state.AutoMode = auto
		for _, sub := range state.Subscriptions {
			for _, candidate := range sub.Nodes {
				if candidate.ID == selected.ID {
					state.ActiveSubscriptionID = sub.ID
					return nil
				}
			}
		}
		return errors.New("selected server was removed while connecting")
	}); err != nil {
		_ = s.xray.Stop()
		return errors.New("could not save VPN state")
	}
	return nil
}

type reachableNode struct {
	node    node.Node
	latency time.Duration
}

func (s *Server) probeCandidates(ctx context.Context, state storage.State, excludeID string) []reachableNode {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	uplink := ""
	if s.config.Tunnel {
		var err error
		uplink, err = netroute.DefaultOutboundInterface()
		if err != nil {
			s.logger.Error("automatic VPN cannot detect the physical WAN interface", "component", "auto", "error", err.Error())
			return nil
		}
	}
	nodes := make([]node.Node, 0)
	for _, sub := range state.Subscriptions {
		for _, candidate := range sub.Nodes {
			if candidate.ID != excludeID {
				nodes = append(nodes, candidate)
			}
		}
	}
	results := make(chan reachableNode, len(nodes))
	semaphore := make(chan struct{}, 4)
	var workers sync.WaitGroup
	for _, current := range nodes {
		current := current
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
			case <-probeCtx.Done():
				return
			}
			defer func() { <-semaphore }()
			latency, err := xray.ProbeWithInterface(probeCtx, s.config.XrayBinary, current, s.config.Tunnel, uplink)
			if err == nil {
				results <- reachableNode{node: current, latency: latency}
			} else {
				s.logger.Warn("automatic VPN server probe failed", "component", "auto", "node", current.ID, "error", err.Error())
			}
		}()
	}
	workers.Wait()
	close(results)
	candidates := make([]reachableNode, 0)
	for candidate := range results {
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].latency < candidates[j].latency })
	return candidates
}

func (s *Server) disconnect(w http.ResponseWriter, _ *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if err := s.xray.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.Update(func(state *storage.State) error { state.VPNEnabled = false; state.AutoMode = false; return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save VPN state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"connected": false})
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	state := s.store.Snapshot()
	selected, ok := selectedNode(state)
	if !ok {
		writeError(w, http.StatusConflict, "select a server before restarting")
		return
	}
	if err := s.applyNode(r.Context(), state, selected, state.AutoMode); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": s.xray.Status().Running})
}

// RunAutoMonitor keeps Auto mode active for the lifetime of the daemon. A failed
// HTTP GET through the current Xray SOCKS tunnel triggers a probe and failover.
func (s *Server) RunAutoMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		state := s.store.Snapshot()
		if !state.VPNEnabled {
			failures = 0
			continue
		}
		if len(state.DeviceMACs) > 0 && s.xray.Status().Running {
			current := append([]string(nil), state.DeviceIPs...)
			current = append(current, devices.Resolve(state.DeviceMACs, devices.Discover())...)
			sort.Strings(current)
			s.operationMu.Lock()
			if !slices.Equal(current, s.activeSources) {
				latest := s.store.Snapshot()
				if selected, ok := selectedNode(latest); ok && latest.VPNEnabled {
					if err := s.applyNode(ctx, latest, selected, latest.AutoMode); err != nil {
						s.logger.Warn("could not update device addresses", "component", "devices", "error", err.Error())
					}
				}
			}
			s.operationMu.Unlock()
		}
		before := s.xray.Status()
		if !before.Running && !state.AutoMode {
			if err := s.RestoreVPN(ctx); err != nil {
				s.logger.Warn("VPN recovery failed", "error", err.Error())
			}
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, 9*time.Second)
		err := health.Check(checkCtx)
		cancel()
		after := s.xray.Status()
		if before.PID != after.PID {
			continue
		}
		s.healthMu.Lock()
		s.healthPID, s.healthOK, s.healthAt = after.PID, err == nil && after.Running, time.Now().UTC()
		s.healthMu.Unlock()
		if !state.AutoMode {
			failures = 0
			continue
		}
		if err == nil {
			failures = 0
			continue
		}
		failures++
		s.logger.Warn("automatic VPN health check failed", "component", "auto", "failure", failures, "error", err.Error())
		if failures < 2 {
			continue
		}
		failures = 0

		s.operationMu.Lock()
		state = s.store.Snapshot()
		if !state.VPNEnabled || !state.AutoMode {
			s.operationMu.Unlock()
			continue
		}
		candidates := s.probeCandidates(ctx, state, "")
		if len(candidates) == 0 {
			s.logger.Error("automatic VPN failover found no working server", "component", "auto")
			s.operationMu.Unlock()
			continue
		}
		best := candidates[0]
		if err := s.applyNode(ctx, state, best.node, true); err != nil {
			s.logger.Error("automatic VPN failover could not apply server", "component", "auto", "server", best.node.ID, "error", err.Error())
		} else {
			s.logger.Info("automatic VPN switched server", "component", "auto", "server", best.node.Name, "latency_ms", best.latency.Milliseconds())
		}
		s.operationMu.Unlock()
	}
}

// RunSubscriptionMonitor refreshes subscriptions at their profile-update-interval
// (hours). The XTLS header is persisted when the subscription is downloaded.
func (s *Server) RunSubscriptionMonitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	retryAfter := make(map[string]time.Time)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().UTC()
		for _, candidate := range s.store.Snapshot().Subscriptions {
			if next := retryAfter[candidate.ID]; now.Before(next) || !subscriptionDue(candidate, now) {
				continue
			}
			s.operationMu.Lock()
			current, ok := findSubscription(s.store.Snapshot(), candidate.ID)
			if !ok || !subscriptionDue(current, now) {
				s.operationMu.Unlock()
				continue
			}
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			updated, err := s.refreshSubscriptionData(refreshCtx, current.ID, false)
			cancel()
			s.operationMu.Unlock()
			if err != nil {
				retryAfter[candidate.ID] = time.Now().UTC().Add(15 * time.Minute)
				s.logger.Warn("automatic subscription refresh failed", "component", "subscription", "subscription", candidate.Name, "error", err.Error())
				continue
			}
			delete(retryAfter, candidate.ID)
			s.logger.Info("subscription refreshed", "component", "subscription", "subscription", updated.Name, "nodes", len(updated.Nodes), "interval_hours", subscriptionIntervalHours(updated))
		}
	}
}

func subscriptionIntervalHours(sub subscription.Subscription) int {
	if sub.UpdateIntervalHours <= 0 {
		return 24
	}
	return sub.UpdateIntervalHours
}

func subscriptionDue(sub subscription.Subscription, now time.Time) bool {
	interval := time.Duration(subscriptionIntervalHours(sub)) * time.Hour
	return sub.UpdatedAt.IsZero() || !now.Before(sub.UpdatedAt.Add(interval))
}

func countNodes(state storage.State) int {
	count := 0
	for _, sub := range state.Subscriptions {
		count += len(sub.Nodes)
	}
	return count
}

func (s *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"platform": s.config.Platform, "architecture": runtime.GOARCH, "version": s.config.Version, "apiListenAddress": s.config.ListenAddress})
}

type subscriptionSummary struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	NodeCount           int       `json:"nodeCount"`
	UpdatedAt           time.Time `json:"updatedAt,omitempty"`
	UpdateIntervalHours int       `json:"updateIntervalHours"`
}

type nodeSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	Address          string `json:"address"`
	Port             uint16 `json:"port"`
	Transport        string `json:"transport"`
	Security         string `json:"security"`
	SubscriptionID   string `json:"subscriptionId,omitempty"`
	SubscriptionName string `json:"subscriptionName,omitempty"`
	Selected         bool   `json:"selected,omitempty"`
}

func findRoutingProfile(state storage.State, id string) (routing.Profile, bool) {
	for _, profile := range state.RoutingProfiles {
		if profile.ID == id && id != "" {
			return profile, true
		}
	}
	return routing.Profile{}, false
}

func summarizeSubscription(sub subscription.Subscription) subscriptionSummary {
	return subscriptionSummary{ID: sub.ID, Name: sub.Name, URL: maskURL(sub.URL), NodeCount: len(sub.Nodes), UpdatedAt: sub.UpdatedAt, UpdateIntervalHours: subscriptionIntervalHours(sub)}
}

func summarizeNode(n node.Node) nodeSummary {
	security := "none"
	if n.TLS != nil {
		security = "tls"
	}
	if n.Reality != nil {
		security = "reality"
	}
	return nodeSummary{ID: n.ID, Name: n.Name, Protocol: string(n.Protocol), Address: n.Address, Port: n.Port, Transport: n.Transport.Type, Security: security}
}

func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "••••••"
	}
	return u.Scheme + "://" + u.Host + "/••••••"
}

func findSubscription(state storage.State, id string) (subscription.Subscription, bool) {
	for _, sub := range state.Subscriptions {
		if sub.ID == id {
			return sub, true
		}
	}
	return subscription.Subscription{}, false
}

func selectedNode(state storage.State) (node.Node, bool) {
	for _, sub := range state.Subscriptions {
		for _, n := range sub.Nodes {
			if n.ID == state.SelectedNodeID {
				return n, true
			}
		}
	}
	return node.Node{}, false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON value")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := neturlParse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross-origin request denied")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func neturlParse(value string) (url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return url.URL{}, errors.New("invalid origin")
	}
	return *u, nil
}

func requestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			if r.Header.Get("Content-Type") != "" && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("http request", "component", "api", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started).String())
	})
}
