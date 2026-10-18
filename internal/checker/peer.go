package checker

import (
	"net"
	"net/http/httptrace"
	"net/netip"
	"sync"
)

// peerWatch remembers the address an HTTP check last connected to, so the
// result can say that this host reached it. A redirect connects again, and
// the latest connection is the one kept.
//
// The connectivity check reads it: a check that connected to a public address
// proves this host was online, whatever the response said, which is the
// evidence that its own targets may be blocked rather than the host offline.
type peerWatch struct {
	mu   sync.Mutex
	addr netip.Addr
}

func (w *peerWatch) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		ConnectDone: func(_, addr string, err error) {
			if err != nil {
				return
			}
			if a := remoteAddr(addr); a.IsValid() {
				w.mu.Lock()
				w.addr = a
				w.mu.Unlock()
			}
		},
	}
}

func (w *peerWatch) peer() netip.Addr {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addr
}

// remoteAddr parses an "ip:port" address, unmapping an IPv4 address written
// in its IPv6 form. Anything else is the zero Addr.
func remoteAddr(s string) netip.Addr {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// connPeer is the remote address of an open connection, or the zero Addr.
func connPeer(conn net.Conn) netip.Addr {
	if conn == nil || conn.RemoteAddr() == nil {
		return netip.Addr{}
	}
	return remoteAddr(conn.RemoteAddr().String())
}
