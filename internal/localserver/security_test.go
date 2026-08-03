package localserver

import (
	"errors"
	"net"
	"testing"
)

func TestValidateListenAddressClosedLoopbackSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		address string
		valid   bool
	}{
		{"127.0.0.1:8765", true}, {"[::1]:8765", true}, {"localhost:8765", true},
		{"0.0.0.0:8765", false}, {"[::]:8765", false}, {"192.168.1.3:8765", false},
		{"example.com:8765", false}, {"/tmp/a2a.sock", false}, {"127.0.0.1", false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.address, func(t *testing.T) {
			t.Parallel()
			err := ValidateListenAddress(test.address)
			if test.valid && err != nil {
				t.Fatalf("ValidateListenAddress() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrNonLoopback) {
				t.Fatalf("error = %v, want ErrNonLoopback", err)
			}
		})
	}
}

func TestServeRefusesNonLoopbackListenerBeforeAccept(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	listener := &fakeListener{addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8765}}
	err := server.Serve(t.Context(), listener)
	if !errors.Is(err, ErrNonLoopback) {
		t.Fatalf("Serve() error = %v", err)
	}
	if listener.accepted {
		t.Fatal("Serve called Accept on a non-loopback listener")
	}
}

type fakeListener struct {
	addr     net.Addr
	accepted bool
}

func (f *fakeListener) Accept() (net.Conn, error) {
	f.accepted = true
	return nil, errors.New("unexpected Accept")
}
func (f *fakeListener) Close() error   { return nil }
func (f *fakeListener) Addr() net.Addr { return f.addr }
