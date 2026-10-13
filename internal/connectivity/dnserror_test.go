package connectivity

import "net"

// netDNSError builds the *net.DNSError shapes the resolver round reads.
type netDNSError struct{ timeout bool }

func (e *netDNSError) err(name string) error {
	return &net.DNSError{Err: "i/o timeout", Name: name, Server: "192.0.2.53:53", IsTimeout: e.timeout}
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, Server: "192.0.2.53:53", IsNotFound: true}
}

func servfail(name string) error {
	return &net.DNSError{Err: "server misbehaving", Name: name, Server: "192.0.2.53:53"}
}
