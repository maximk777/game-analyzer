package sfs

import (
	"io"

	"github.com/google/gopacket"
	"poker-game-analyzer/pkg/table"
)

// Live turns the packet stream into a sequence of table states. It is the wire
// equivalent of the frame-reading LiveAgent: it owns the table's reconstruction
// and calls OnState with a fresh HandState whenever something observable
// changed, so a caller can feed the advisor without knowing anything about
// packets or SFS messages.
//
// A single table is assumed (the user plays one at a time): one roster,
// regardless of how many connections or other tables the stream carries. The
// hero is identified by configured id or name, never guessed from the wire.
type Live struct {
	HeroID   int64
	HeroName string

	// OnState is called with the current table state after any change. It is
	// never called with nil. OnMessage, if set, sees every decoded message
	// first -- for surfacing dealer chat and showdowns the state does not hold.
	OnState   func(*table.HandState)
	OnMessage func(Message)

	roster *Roster
}

// newRoster makes the roster the source feeds, carrying the hero config.
func (l *Live) newRoster() *Roster {
	r := NewRoster()
	r.HeroID = l.HeroID
	r.HeroName = l.HeroName
	return r
}

// Run reads a pcap stream (a file, or tcpdump's stdout) to completion, driving
// OnState as the table changes. ports is the detected game port set; empty falls
// back to the range.
func (l *Live) Run(r io.Reader, ports []int) error {
	l.roster = l.newRoster()
	return ReadPorts(r, ports, l.consumer)
}

// consumer builds a scanner for each connection; all of them feed the one
// roster, because it is one table.
func (l *Live) consumer(flow gopacket.Flow) StreamConsumer {
	return NewScanner(flow, l.handle)
}

func (l *Live) handle(_ gopacket.Flow, _ Direction, m Message) {
	if l.OnMessage != nil {
		l.OnMessage(m)
	}
	changed := l.roster.Apply(m)
	if changed && l.OnState != nil {
		if hs := l.roster.HandState(); hs != nil {
			l.OnState(hs)
		}
	}
}

// Roster exposes the current reconstruction, for callers that want the raw
// roster (the sniffer's textual view) rather than a HandState.
func (l *Live) Roster() *Roster { return l.roster }
