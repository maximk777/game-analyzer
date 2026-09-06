package sfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/google/gopacket"
)

// maxObjBytes bounds how far the scanner will chase a single '{' before giving
// up on it. Every game message is well under two kilobytes; a '{' that has not
// closed into valid JSON within this many bytes was a byte of binary framing
// that merely looked like the start of an object, and chasing it further would
// let one stray brace pin the whole buffer open.
const maxObjBytes = 32 * 1024

// MessageHandler receives each decoded game message, tagged with the connection
// it came from (one flow per table) and the direction it travelled.
type MessageHandler func(flow gopacket.Flow, dir Direction, m Message)

// Scanner is a StreamConsumer that pulls JSON game messages out of the raw
// reassembled bytes of one connection. It holds a buffer per direction because
// a message split across packets must be rejoined before it can be parsed, and
// the two directions split at different boundaries.
//
// The extraction is resynchronising by design: the wire is SFS2X binary framing
// with JSON blobs inside it, and rather than decode the framing we look for the
// blobs directly. A byte of framing that happens to be '{' starts a parse that
// fails, and the scanner steps past it to the next candidate -- so a corrupt or
// unrecognised region costs the messages inside it and nothing after.
type Scanner struct {
	flow   gopacket.Flow
	handle MessageHandler
	raw    RawHandler
	bufs   [2][]byte // indexed by Direction
}

// RawHandler, if set, receives every valid JSON object the scanner lifts out of
// the stream, before classification -- recognised or not. It exists for
// protocol discovery: a message whose keys we do not yet model is invisible to
// the MessageHandler but shows up here.
type RawHandler func(flow gopacket.Flow, dir Direction, obj []byte)

// NewScanner makes a Scanner for one connection.
func NewScanner(flow gopacket.Flow, h MessageHandler) *Scanner {
	return &Scanner{flow: flow, handle: h}
}

// SetRawHandler attaches a RawHandler for protocol discovery.
func (s *Scanner) SetRawHandler(h RawHandler) { s.raw = h }

// Feed takes the next run of reassembled bytes for a direction, appends it to
// that direction's buffer, and extracts every complete message now available.
func (s *Scanner) Feed(dir Direction, b []byte) {
	s.bufs[dir] = append(s.bufs[dir], b...)
	s.bufs[dir] = s.extract(dir, s.bufs[dir])
}

// Close drops both buffers. Anything still unparsed at connection teardown was
// an incomplete tail with no more bytes coming, and is not a message.
func (s *Scanner) Close() {
	s.bufs[ServerToClient] = nil
	s.bufs[ClientToServer] = nil
}

// extract consumes every complete JSON object it can find in buf, emitting the
// recognised ones, and returns the unconsumed tail to be retained for the next
// Feed. The tail is either empty (buffer fully drained) or begins at a '{' that
// is still waiting for more bytes to complete.
func (s *Scanner) extract(dir Direction, buf []byte) []byte {
	from := 0
	for {
		i := bytes.IndexByte(buf[from:], '{')
		if i < 0 {
			// No candidate object start remains; keep nothing.
			return nil
		}
		i += from

		raw, end, status := decodeAt(buf[i:])
		switch status {
		case scanOK:
			if s.raw != nil {
				s.raw(s.flow, dir, raw)
			}
			if m, ok := decodeBlob(raw); ok {
				s.handle(s.flow, dir, m)
			}
			from = i + end
		case scanIncomplete:
			// The object may still be arriving. Retain from this '{' unless it
			// has already run past the size any real message reaches, in which
			// case it was a false start and we step over it.
			if len(buf)-i > maxObjBytes {
				from = i + 1
				continue
			}
			return buf[i:]
		case scanNotJSON:
			// This '{' does not begin valid JSON; step past it.
			from = i + 1
		}
	}
}

type scanStatus int

const (
	scanOK scanStatus = iota
	scanIncomplete
	scanNotJSON
)

// decodeAt tries to read one complete JSON value starting at b[0] (which the
// caller guarantees is '{'). On success it returns the raw object bytes and the
// offset just past them. scanIncomplete means the input ends inside a
// well-formed-so-far value and more bytes are needed; scanNotJSON means the
// bytes here are not a JSON object at all.
func decodeAt(b []byte) (raw json.RawMessage, end int, status scanStatus) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, 0, scanIncomplete
		}
		return nil, 0, scanNotJSON
	}
	return raw, int(dec.InputOffset()), scanOK
}
