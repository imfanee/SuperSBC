package sipcapture

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// WritePCAP writes the messages as a libpcap file (Ethernet, IPv4, UDP or
// TCP) for Wireshark and sngrep.
//
// What is exact: the SIP payload byte for byte as FreeSWITCH saw it on the
// wire (decrypted for TLS), the source and destination addresses and ports,
// the transport, and the capture timestamps (microseconds). What is
// synthesised: HEP carries no layer 2 or 3 headers, so the Ethernet header
// has zero MAC addresses and the IP/UDP/TCP headers are rebuilt (TTL 64,
// sequential IP ids, TCP sequence numbers that follow the payload lengths
// per direction so Wireshark reassembles the stream; UDP checksum 0).
// Legs filter: "customer", "carrier" or "" for all.
func WritePCAP(w io.Writer, msgs []Message, leg string) error {
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:], 0xa1b2c3d4) // magic, microsecond timestamps
	binary.LittleEndian.PutUint16(hdr[4:], 2)          // version 2.4
	binary.LittleEndian.PutUint16(hdr[6:], 4)
	binary.LittleEndian.PutUint32(hdr[16:], 262144) // snaplen
	binary.LittleEndian.PutUint32(hdr[20:], 1)      // LINKTYPE_ETHERNET
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	seq := map[string]uint32{} // TCP sequence per "src:port>dst:port"
	var ipID uint16
	for _, m := range msgs {
		if leg != "" && m.Leg != leg {
			continue
		}
		ipID++
		frame, err := frameFor(m, ipID, seq)
		if err != nil {
			continue // not IPv4: skipped rather than written wrongly
		}
		rec := make([]byte, 16)
		ts := m.Time.UnixMicro()
		binary.LittleEndian.PutUint32(rec[0:], uint32(ts/1e6))
		binary.LittleEndian.PutUint32(rec[4:], uint32(ts%1e6))
		binary.LittleEndian.PutUint32(rec[8:], uint32(len(frame)))
		binary.LittleEndian.PutUint32(rec[12:], uint32(len(frame)))
		if _, err := w.Write(rec); err != nil {
			return err
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
	}
	return nil
}

// frameFor builds Ethernet + IPv4 + UDP/TCP around the exact SIP bytes.
func frameFor(m Message, ipID uint16, seq map[string]uint32) ([]byte, error) {
	src := net.ParseIP(m.SrcIP).To4()
	dst := net.ParseIP(m.DstIP).To4()
	if src == nil || dst == nil {
		return nil, fmt.Errorf("not ipv4: %s -> %s", m.SrcIP, m.DstIP)
	}
	payload := []byte(m.Raw)
	tcp := m.Transport != "udp"
	l4 := 8
	proto := byte(17)
	if tcp {
		l4 = 20
		proto = 6
	}
	ipLen := 20 + l4 + len(payload)
	frame := make([]byte, 14+ipLen)
	binary.BigEndian.PutUint16(frame[12:], 0x0800) // Ethernet type IPv4, zero MACs
	ip := frame[14:]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(ipLen))
	binary.BigEndian.PutUint16(ip[4:], ipID)
	ip[8] = 64
	ip[9] = proto
	copy(ip[12:16], src)
	copy(ip[16:20], dst)
	binary.BigEndian.PutUint16(ip[10:], ipChecksum(ip[:20]))
	l4b := ip[20:]
	binary.BigEndian.PutUint16(l4b[0:], uint16(m.SrcPort))
	binary.BigEndian.PutUint16(l4b[2:], uint16(m.DstPort))
	if tcp {
		fwd := fmt.Sprintf("%s:%d>%s:%d", m.SrcIP, m.SrcPort, m.DstIP, m.DstPort)
		rev := fmt.Sprintf("%s:%d>%s:%d", m.DstIP, m.DstPort, m.SrcIP, m.SrcPort)
		if _, ok := seq[fwd]; !ok {
			seq[fwd] = 1
		}
		binary.BigEndian.PutUint32(l4b[4:], seq[fwd]) // sequence
		binary.BigEndian.PutUint32(l4b[8:], seq[rev]) // acknowledgement
		l4b[12] = 5 << 4                              // data offset 20 bytes
		l4b[13] = 0x18                                // PSH, ACK
		binary.BigEndian.PutUint16(l4b[14:], 65535)   // window
		seq[fwd] += uint32(len(payload))
		copy(l4b[20:], payload)
		binary.BigEndian.PutUint16(l4b[16:], tcpChecksum(src, dst, l4b[:20+len(payload)]))
	} else {
		binary.BigEndian.PutUint16(l4b[4:], uint16(8+len(payload)))
		copy(l4b[8:], payload) // UDP checksum left 0 (not computed)
	}
	return frame, nil
}

func ipChecksum(b []byte) uint16 { return finish(sum(b, 0)) }

// tcpChecksum covers the pseudo header and the segment.
func tcpChecksum(src, dst net.IP, seg []byte) uint16 {
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], src)
	copy(pseudo[4:8], dst)
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:], uint16(len(seg)))
	return finish(sum(seg, sum(pseudo, 0)))
}

func sum(b []byte, acc uint32) uint32 {
	for i := 0; i+1 < len(b); i += 2 {
		acc += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		acc += uint32(b[len(b)-1]) << 8
	}
	return acc
}

func finish(acc uint32) uint16 {
	for acc>>16 != 0 {
		acc = (acc & 0xffff) + (acc >> 16)
	}
	return ^uint16(acc)
}
