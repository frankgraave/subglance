package checker

import (
	"net/netip"
	"testing"
)

// ICMP errors quote the datagram that caused them. Matching on that quote is
// what stops an unrelated flow's "destination unreachable" — which a raw
// socket also receives — from failing a healthy monitor.
func TestErrorRefersToOurProbe(t *testing.T) {
	target := netip.MustParseAddr("192.0.2.10")
	other := netip.MustParseAddr("198.51.100.7")

	const (
		ourID  = 4242
		ourSeq = 1234
	)

	// buildIPv4Quote assembles the quoted portion of an ICMP error: a minimal
	// IPv4 header followed by the first eight bytes of the echo request.
	buildIPv4Quote := func(dst netip.Addr, id, seq int) []byte {
		q := make([]byte, 28)
		q[0] = 0x45 // version 4, IHL 5 words = 20 bytes
		copy(q[16:20], dst.AsSlice())

		q[20] = 8 // echo request
		q[24] = byte(id >> 8)
		q[25] = byte(id)
		q[26] = byte(seq >> 8)
		q[27] = byte(seq)
		return q
	}

	tests := []struct {
		name  string
		quote []byte
		mode  pingMode
		want  bool
	}{
		{
			name:  "our probe on a raw socket",
			quote: buildIPv4Quote(target, ourID, ourSeq),
			mode:  pingModeRaw,
			want:  true,
		},
		{
			name:  "our probe on an unprivileged socket",
			quote: buildIPv4Quote(target, 9999, ourSeq), // kernel rewrote the ID
			mode:  pingModeUnprivileged,
			want:  true,
		},
		{
			// The case that matters: someone else's traffic.
			name:  "another process's probe, same target",
			quote: buildIPv4Quote(target, 9999, ourSeq),
			mode:  pingModeRaw,
			want:  false,
		},
		{
			name:  "our ID but a different sequence",
			quote: buildIPv4Quote(target, ourID, 4321),
			mode:  pingModeRaw,
			want:  false,
		},
		{
			name:  "a probe to a different host",
			quote: buildIPv4Quote(other, ourID, ourSeq),
			mode:  pingModeRaw,
			want:  false,
		},
		{
			name:  "truncated quote",
			quote: buildIPv4Quote(target, ourID, ourSeq)[:22],
			mode:  pingModeRaw,
			want:  false,
		},
		{
			name:  "empty quote",
			quote: nil,
			mode:  pingModeRaw,
			want:  false,
		},
		{
			name:  "nonsense header length",
			quote: append([]byte{0x40}, make([]byte, 27)...), // IHL 0
			mode:  pingModeRaw,
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errorRefersToOurProbe(tc.quote, target, ourID, ourSeq, tc.mode)
			if got != tc.want {
				t.Errorf("errorRefersToOurProbe = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestErrorRefersToOurProbeIPv6(t *testing.T) {
	target := netip.MustParseAddr("2001:db8::1")

	quote := make([]byte, 48)
	copy(quote[24:40], target.AsSlice())
	quote[40] = 128 // echo request
	quote[44] = 0x10
	quote[45] = 0x92 // id 4242
	quote[46] = 0x04
	quote[47] = 0xd2 // seq 1234

	if !errorRefersToOurProbe(quote, target, 4242, 1234, pingModeRaw) {
		t.Error("a matching IPv6 quote was not recognised")
	}

	// Truncated below the fixed header plus payload.
	if errorRefersToOurProbe(quote[:44], target, 4242, 1234, pingModeRaw) {
		t.Error("a truncated IPv6 quote should not match")
	}
}
