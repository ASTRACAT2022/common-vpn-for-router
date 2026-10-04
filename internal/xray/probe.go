package xray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"common-vpn-router/internal/node"
)

const probeTimeout = 7 * time.Second

// Probe measures a node with an HTTPS GET through a temporary Xray process.
// It does not alter the active VPN process.
func Probe(ctx context.Context, binary string, selected node.Node, tunnelMode bool) (time.Duration, error) {
	return ProbeWithInterface(ctx, binary, selected, tunnelMode, "")
}

// ProbeWithInterface measures a node while binding Xray's uplink to the
// physical interface that carries the router's WAN default route.
func ProbeWithInterface(ctx context.Context, binary string, selected node.Node, tunnelMode bool, outboundInterface string) (time.Duration, error) {
	if err := selected.Validate(); err != nil {
		return 0, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	port, err := freeLocalPort()
	if err != nil {
		return 0, errors.New("could not reserve a local probe port")
	}
	content, err := GenerateWithOptions(selected, Options{SocksPort: port, ProbeOnly: true, Tunnel: tunnelMode, OutboundInterface: outboundInterface})
	if err != nil {
		return 0, errors.New("could not generate probe configuration")
	}
	binaryPath, err := exec.LookPath(binary)
	if err != nil {
		return 0, errors.New("Xray executable was not found")
	}
	dir, err := os.MkdirTemp("", "common-vpn-probe-")
	if err != nil {
		return 0, errors.New("could not create temporary probe directory")
	}
	defer os.RemoveAll(dir)
	configPath := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		return 0, errors.New("could not write temporary probe configuration")
	}
	command := exec.CommandContext(probeCtx, binaryPath, "run", "-config", configPath)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		return 0, errors.New("could not start Xray probe")
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = command.Wait(); close(done) }()
	defer stopProbe(command, done)

	if err := waitForListener(probeCtx, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), done); err != nil {
		if waitErr != nil {
			return 0, errors.New("Xray rejected the probe configuration")
		}
		return 0, errors.New("Xray probe did not start")
	}

	started := time.Now()
	if err := CheckHTTPGet(probeCtx, net.JoinHostPort("127.0.0.1", fmt.Sprint(port))); err != nil {
		return 0, fmt.Errorf("server did not complete the HTTPS health check: %w", err)
	}
	return time.Since(started), nil
}

func waitForListener(ctx context.Context, address string, done <-chan struct{}) error {
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-done:
			return errors.New("Xray process exited")
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("Xray listener did not start")
		case <-ticker.C:
		}
	}
}

func freeLocalPort() (uint16, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return uint16(listener.Addr().(*net.TCPAddr).Port), nil
}

// DialSOCKS5 opens a SOCKS5 tunnel to targetAddress using the supplied local proxy.
func DialSOCKS5(ctx context.Context, proxyAddress, targetAddress string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	if err := socksHandshake(ctx, conn, targetAddress); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func socksHandshake(ctx context.Context, conn net.Conn, targetAddress string) error {
	var err error
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(probeTimeout))
	}
	if err := writeAll(conn, []byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil {
		return err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		return errors.New("SOCKS authentication negotiation failed")
	}
	host, portText, err := net.SplitHostPort(targetAddress)
	if err != nil {
		return err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid SOCKS target port")
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, 0x01)
			request = append(request, ipv4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return errors.New("invalid SOCKS target host")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = append(request, byte(port>>8), byte(port))
	if err := writeAll(conn, request); err != nil {
		return err
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 0x05 || response[1] != 0x00 || response[2] != 0x00 {
		return errors.New("proxy connection was rejected")
	}
	switch response[3] {
	case 0x01:
		_, err = io.CopyN(io.Discard, conn, 4)
	case 0x03:
		var size [1]byte
		if _, err = io.ReadFull(conn, size[:]); err == nil {
			_, err = io.CopyN(io.Discard, conn, int64(size[0]))
		}
	case 0x04:
		_, err = io.CopyN(io.Discard, conn, 16)
	default:
		return errors.New("proxy returned an invalid address type")
	}
	if err != nil {
		return err
	}
	var portBytes [2]byte
	_, err = io.ReadFull(conn, portBytes[:])
	if err != nil {
		return err
	}
	return conn.SetDeadline(time.Time{})
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

// CheckHTTPGet verifies an HTTPS endpoint through a running local Xray SOCKS inbound.
func CheckHTTPGet(ctx context.Context, socksAddress string) error {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return DialSOCKS5(ctx, socksAddress, address)
		},
		ForceAttemptHTTP2: false,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	var lastErr error
	for _, endpoint := range []string{"https://cp.cloudflare.com/generate_204", "https://www.gstatic.com/generate_204"} {
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusOK {
				cancel()
				return nil
			}
			lastErr = errors.New("health endpoint returned an unexpected status")
		} else {
			lastErr = err
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func stopProbe(command *exec.Cmd, done <-chan struct{}) {
	if command.Process == nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	select {
	case <-done:
		return
	case <-time.After(300 * time.Millisecond):
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}
