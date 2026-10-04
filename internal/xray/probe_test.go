package xray

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestSocksConnectAcceptsSuccessfulTunnelHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(server, greeting); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0x00})
		request := make([]byte, 10)
		if _, err := io.ReadFull(server, request); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0x00, 0x00, 0x01, 1, 1, 1, 1, 1, 187})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := socksHandshake(ctx, client, "1.1.1.1:443"); err != nil {
		t.Fatalf("SOCKS probe failed: %v", err)
	}
}

func TestSocksConnectRejectsUnhealthyTunnel(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(server, greeting); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0x00})
		request := make([]byte, 10)
		if _, err := io.ReadFull(server, request); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := socksHandshake(ctx, client, "1.1.1.1:443"); err == nil {
		t.Fatal("expected failed proxy connect to be rejected")
	}
}
