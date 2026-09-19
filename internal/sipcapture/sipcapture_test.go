package sipcapture

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestFirstLine(t *testing.T) {
	l, m := firstLine("INVITE sip:442071234567@172.28.0.10:5060 SIP/2.0\r\nVia: x\r\n")
	if l != "INVITE sip:442071234567@172.28.0.10:5060 SIP/2.0" || m != "INVITE" {
		t.Fatal(l, m)
	}
	l, m = firstLine("SIP/2.0 183 Session Progress\r\n")
	if m != "183" || l != "SIP/2.0 183 Session Progress" {
		t.Fatal(l, m)
	}
	if !sameHost("172.28.0.101", "172.28.0.101") || sameHost("", "x") || !sameHost("10.0.0.1", "10.0.0.1:5060") {
		t.Fatal("sameHost")
	}
	if New("").Enabled() {
		t.Fatal("empty url must be disabled")
	}
}

func TestWritePCAP(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 500000, time.UTC)
	msgs := []Message{
		{Time: now, SrcIP: "198.51.100.10", SrcPort: 5060, DstIP: "203.0.113.5", DstPort: 5060, Transport: "udp", Leg: "customer", Raw: "INVITE sip:1@x SIP/2.0\r\nCall-ID: a\r\n\r\n"},
		{Time: now.Add(time.Millisecond), SrcIP: "203.0.113.5", SrcPort: 5080, DstIP: "192.0.2.1", DstPort: 5060, Transport: "tcp", Leg: "carrier", Raw: "INVITE sip:1@y SIP/2.0\r\n\r\n"},
		{Time: now.Add(2 * time.Millisecond), SrcIP: "192.0.2.1", SrcPort: 5060, DstIP: "203.0.113.5", DstPort: 5080, Transport: "tcp", Leg: "carrier", Raw: "SIP/2.0 100 Trying\r\n\r\n"},
		{Time: now, SrcIP: "2001:db8::1", SrcPort: 5060, DstIP: "203.0.113.5", DstPort: 5060, Transport: "udp", Leg: "customer", Raw: "x"},
	}
	var buf bytes.Buffer
	if err := WritePCAP(&buf, msgs, ""); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if binary.LittleEndian.Uint32(b[0:]) != 0xa1b2c3d4 || binary.LittleEndian.Uint32(b[20:]) != 1 {
		t.Fatal("pcap header")
	}
	// walk the records: three IPv4 frames (one UDP, two TCP), the IPv6 one skipped
	off, n := 24, 0
	var payloads []string
	var tcpSeqs []uint32
	for off < len(b) {
		incl := int(binary.LittleEndian.Uint32(b[off+8:]))
		frame := b[off+16 : off+16+incl]
		if binary.BigEndian.Uint16(frame[12:]) != 0x0800 || frame[14] != 0x45 {
			t.Fatal("frame headers")
		}
		ipLen := int(binary.BigEndian.Uint16(frame[16:]))
		if 14+ipLen != incl {
			t.Fatalf("ip length %d vs frame %d", ipLen, incl)
		}
		if ipChecksum(frame[14:34]) != 0 {
			t.Fatal("ip checksum does not verify")
		}
		proto := frame[14+9]
		var payload string
		switch proto {
		case 17:
			payload = string(frame[14+28:])
		case 6:
			seg := frame[34:]
			if tcpChecksum(frame[26:30], frame[30:34], seg) != 0 {
				t.Fatal("tcp checksum does not verify")
			}
			tcpSeqs = append(tcpSeqs, binary.BigEndian.Uint32(seg[4:]))
			payload = string(seg[20:])
		default:
			t.Fatalf("protocol %d", proto)
		}
		payloads = append(payloads, payload)
		off += 16 + incl
		n++
	}
	if n != 3 || payloads[0] != msgs[0].Raw || payloads[1] != msgs[1].Raw || payloads[2] != msgs[2].Raw {
		t.Fatalf("frames %d payloads %q", n, payloads)
	}
	if len(tcpSeqs) != 2 || tcpSeqs[0] != 1 || tcpSeqs[1] != 1 {
		t.Fatalf("tcp sequence numbers %v", tcpSeqs)
	}
	ts := binary.LittleEndian.Uint32(b[24:])
	us := binary.LittleEndian.Uint32(b[28:])
	if int64(ts) != now.Unix() || us != 500 {
		t.Fatalf("timestamp %d.%d", ts, us)
	}
	buf.Reset()
	_ = WritePCAP(&buf, msgs, "customer")
	if len(buf.Bytes()) != 24+16+14+20+8+len(msgs[0].Raw) {
		t.Fatalf("customer only size %d", buf.Len())
	}
}
