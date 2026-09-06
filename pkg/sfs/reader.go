// Package sfs reads CoinPoker's game-server traffic off the wire and turns it
// into table events.
//
// The client speaks SmartFoxServer2X to the game server on TCP :7000, and on
// this operator that stream is plaintext: length-prefixed SFSObject frames that
// carry readable JSON blobs. We do not decode the SFSObject envelope -- the
// blobs are self-describing and the framing is not needed to find them. What we
// do need is a faithful reassembly of the TCP byte stream, because a single
// frame is routinely split across packets and two frames share one, and a JSON
// object cut in half by a packet boundary is a JSON object lost.
//
// The reader is deliberately transport-only: it hands whole reassembled byte
// runs to a StreamConsumer and knows nothing about what they mean. Decoding
// lives in decode.go, table state in roster.go. That split is the same one the
// vision pipeline draws between perception and state -- reassembly is not the
// decoder's job any more than the frame grabber was the stabilizer's.
package sfs

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"github.com/google/gopacket/tcpassembly"
)

// GamePortLo and GamePortHi bound the SmartFoxServer port range. The port is
// assigned per table by the lobby (seen live on :7000, :7002, :7003), so the
// reader matches a range rather than one port; the whole range is plaintext
// SFS2X, while the legacy :9000 and the TLS :443 fronts carry nothing we read.
const (
	GamePortLo layers.TCPPort = 7000
	GamePortHi layers.TCPPort = 7010
)

// inGameRange reports whether a TCP port is in the SmartFoxServer range. It is
// the fallback matcher, used when no specific ports were detected from the live
// client.
func inGameRange(p layers.TCPPort) bool { return p >= GamePortLo && p <= GamePortHi }

// PortMatch decides whether a TCP port carries game traffic. Detected ports
// give an exact matcher; absent that, the range is used.
type PortMatch func(layers.TCPPort) bool

// MatchPorts builds a matcher for a specific set of ports (from DetectGamePorts).
// With no ports it falls back to the range, so a caller can always pass the
// detector's result straight through.
func MatchPorts(ports []int) PortMatch {
	if len(ports) == 0 {
		return inGameRange
	}
	set := make(map[layers.TCPPort]bool, len(ports))
	for _, p := range ports {
		set[layers.TCPPort(p)] = true
	}
	return func(p layers.TCPPort) bool { return set[p] }
}

// StreamConsumer receives reassembled bytes from one direction of one TCP
// connection, in order, with the gaps that reassembly could not fill already
// resolved. dir says which way the bytes flowed; server-to-client carries the
// game state, client-to-server the local player's raw actions.
//
// Feed may be called many times for one connection as more bytes arrive; the
// consumer is expected to hold whatever tail it could not yet parse. Close is
// called once when the connection ends, so a consumer can drop its buffer.
type StreamConsumer interface {
	Feed(dir Direction, b []byte)
	Close()
}

// Direction is which endpoint sent the bytes.
type Direction int

const (
	// ServerToClient is the game server talking: seats, stacks, actions, pot,
	// turn, showdown -- everything we want.
	ServerToClient Direction = iota
	// ClientToServer is the local client talking: our own actions, mostly, and
	// keepalives. Kept because it is free and occasionally disambiguating.
	ClientToServer
)

func (d Direction) String() string {
	if d == ServerToClient {
		return "s->c"
	}
	return "c->s"
}

// ConsumerFactory makes a fresh StreamConsumer for each new TCP connection to
// the game port. One connection is one table; a session on several tables opens
// several connections, so the caller gets one consumer per table for free.
type ConsumerFactory func(flow gopacket.Flow) StreamConsumer

// Read consumes a pcap stream -- as written by `tcpdump -U -w -` -- from r,
// reassembles every TCP connection touching GamePort, and drives a consumer per
// connection. It returns when r is exhausted (EOF) or on a read error.
//
// The pcap is parsed in pure Go (pcapgo), so this needs no libpcap and no cgo;
// the only privileged step is tcpdump's own capture, which is why capture and
// decode are separate processes joined by a pipe.
func Read(r io.Reader, factory ConsumerFactory) error {
	return ReadPorts(r, nil, factory)
}

// ReadPorts is Read with an explicit set of game ports, as returned by
// DetectGamePorts. An empty set falls back to the SmartFoxServer port range.
func ReadPorts(r io.Reader, ports []int, factory ConsumerFactory) error {
	match := MatchPorts(ports)
	pr, err := pcapgo.NewReader(r)
	if err != nil {
		return fmt.Errorf("open pcap stream: %w", err)
	}

	pool := tcpassembly.NewStreamPool(&streamFactory{make: factory, match: match})
	asm := tcpassembly.NewAssembler(pool)

	// Periodic flush is what makes a LIVE stream work. The reassembler holds a
	// segment while it waits for an earlier one -- and on a connection we joined
	// mid-hand (the table was already open when capture started) that earlier
	// one was never captured, so without a flush the data waits forever and the
	// HUD stays blank. A file ends and FlushAll drains it; a live stream never
	// ends, so we force delivery of anything held longer than flushGap. That is
	// also what bounds the assembler's memory.
	// flushGap is small on purpose: the data the reassembler holds mid-stream
	// arrives with a current timestamp, so a large gap would keep it waiting.
	// A quarter second is long enough to let genuinely out-of-order segments on
	// a LAN arrive first, short enough that the HUD is not visibly behind.
	const flushEvery = 100 * time.Millisecond
	const flushGap = 250 * time.Millisecond
	var lastFlush time.Time

	linkType := pr.LinkType()
	for {
		data, ci, err := pr.ReadPacketData()
		if err == io.EOF {
			asm.FlushAll()
			return nil
		}
		if err != nil {
			return fmt.Errorf("read packet: %w", err)
		}

		pkt := gopacket.NewPacket(data, linkType, gopacket.NoCopy)
		tcpLayer := pkt.Layer(layers.LayerTypeTCP)
		if tcpLayer == nil {
			continue
		}
		tcp, _ := tcpLayer.(*layers.TCP)
		if !match(tcp.SrcPort) && !match(tcp.DstPort) {
			continue
		}
		net := pkt.NetworkLayer()
		if net == nil {
			continue
		}
		asm.AssembleWithTimestamp(net.NetworkFlow(), tcp, ci.Timestamp)

		if lastFlush.IsZero() {
			lastFlush = ci.Timestamp
		} else if ci.Timestamp.Sub(lastFlush) >= flushEvery {
			asm.FlushOlderThan(ci.Timestamp.Add(-flushGap))
			lastFlush = ci.Timestamp
		}
	}
}

// streamFactory adapts our ConsumerFactory to gopacket's tcpassembly, making one
// stream per direction of each connection and routing reassembled bytes to the
// right consumer. Both directions of one connection share a single consumer so
// the decoder sees one table whole.
type streamFactory struct {
	make  ConsumerFactory
	match PortMatch
	// byConn maps a connection (keyed by its endpoint-ordered flow) to the one
	// consumer shared by both of its directions.
	byConn map[string]StreamConsumer
}

func (f *streamFactory) New(netFlow, tcpFlow gopacket.Flow) tcpassembly.Stream {
	if f.byConn == nil {
		f.byConn = make(map[string]StreamConsumer)
	}
	// FastHash is direction-independent, so both directions land on one key.
	key := fmt.Sprintf("%d-%d", netFlow.FastHash(), tcpFlow.FastHash())
	consumer, ok := f.byConn[key]
	if !ok {
		consumer = f.make(netFlow)
		f.byConn[key] = consumer
	}

	dir := ClientToServer
	// The server sends from a game port; that side is source on server->client.
	if src, err := strconv.Atoi(tcpFlow.Src().String()); err == nil && f.match(layers.TCPPort(src)) {
		dir = ServerToClient
	}
	return &stream{consumer: consumer, dir: dir, key: key, factory: f}
}

// stream is one direction of one connection.
type stream struct {
	consumer StreamConsumer
	dir      Direction
	key      string
	factory  *streamFactory
	// closed counts how many of the two directions have ended, so the consumer
	// is closed once, after both.
	done bool
}

func (s *stream) Reassembled(rs []tcpassembly.Reassembly) {
	for _, r := range rs {
		if len(r.Bytes) == 0 {
			continue
		}
		s.consumer.Feed(s.dir, r.Bytes)
	}
}

func (s *stream) ReassemblyComplete() {
	if s.done {
		return
	}
	s.done = true
	// Close the shared consumer when the first direction completes; the table
	// is over once either half of the connection tears down.
	if c, ok := s.factory.byConn[s.key]; ok {
		delete(s.factory.byConn, s.key)
		c.Close()
	}
}
