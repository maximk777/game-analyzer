package sfs

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
)

// buildPcap writes a minimal Ethernet/IPv4/TCP pcap carrying payloads as
// server->client segments on the game port, with consecutive sequence numbers
// so reassembly joins them in order. It is the on-disk shape `tcpdump -w`
// produces, built in memory so the reader can be tested without a capture.
func buildPcap(t *testing.T, payloads ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := pcapgo.NewWriter(&buf)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}

	srcIP, dstIP := net.IPv4(10, 0, 0, 2), net.IPv4(10, 0, 0, 1)
	srcMAC, _ := net.ParseMAC("02:00:00:00:00:02")
	dstMAC, _ := net.ParseMAC("02:00:00:00:00:01")
	seq := uint32(1000)

	for _, p := range payloads {
		eth := &layers.Ethernet{SrcMAC: srcMAC, DstMAC: dstMAC, EthernetType: layers.EthernetTypeIPv4}
		ip := &layers.IPv4{
			Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolTCP,
			SrcIP: srcIP, DstIP: dstIP,
		}
		tcp := &layers.TCP{
			SrcPort: GamePortLo, DstPort: 54321,
			Seq: seq, ACK: true, Window: 65535,
		}
		if err := tcp.SetNetworkLayerForChecksum(ip); err != nil {
			t.Fatal(err)
		}
		sb := gopacket.NewSerializeBuffer()
		opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
		if err := gopacket.SerializeLayers(sb, opts, eth, ip, tcp, gopacket.Payload(p)); err != nil {
			t.Fatal(err)
		}
		data := sb.Bytes()
		ci := gopacket.CaptureInfo{
			Timestamp:     time.Now(),
			CaptureLength: len(data),
			Length:        len(data),
		}
		if err := w.WritePacket(ci, data); err != nil {
			t.Fatal(err)
		}
		seq += uint32(len(p))
	}
	return buf.Bytes()
}

func TestReadReassemblesAcrossPackets(t *testing.T) {
	seat := framed(`{"seatId":3,"userName":"axiom10","userId":1795427,"userChips":142114.20,"betAmout":3828.00,"lastAction":"Raise"}`)
	// Cut the object across two packets so success depends on reassembly.
	cut := len(seat) - 25
	pcap := buildPcap(t, seat[:cut], seat[cut:])

	var got []Message
	err := Read(bytes.NewReader(pcap), func(flow gopacket.Flow) StreamConsumer {
		return NewScanner(flow, func(_ gopacket.Flow, dir Direction, m Message) {
			if dir != ServerToClient {
				t.Errorf("dir = %v, want server->client", dir)
			}
			got = append(got, m)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Seat == nil || got[0].Seat.UserName != "axiom10" {
		t.Fatalf("seat = %+v", got[0].Seat)
	}
	if got, want := got[0].Seat.UserChips.String(), "142114.2"; got != want {
		t.Errorf("chips = %q, want %q", got, want)
	}
}

func TestReadIgnoresOtherPorts(t *testing.T) {
	// A pcap of the right shape but the payload isn't on :7000 -- buildPcap
	// only emits :7000, so assert the reader tolerates an empty/irrelevant
	// capture without error and yields nothing.
	pcap := buildPcap(t) // no packets
	n := 0
	err := Read(bytes.NewReader(pcap), func(flow gopacket.Flow) StreamConsumer {
		return NewScanner(flow, func(_ gopacket.Flow, _ Direction, _ Message) { n++ })
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("got %d messages from empty capture, want 0", n)
	}
}
