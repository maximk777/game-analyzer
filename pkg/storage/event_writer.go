package storage

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"poker-game-analyzer/pkg/table"
)

// EventWriter takes events off the capture path and puts them in the database.
//
// The capture loop must never wait on a disk. It reads the table several times
// a second and a frame it does not take is a frame gone; a write that blocks it
// costs an action, not a millisecond. So appending is a send on a buffered
// channel and the database is somebody else's problem.
//
// When the queue does fill -- the disk stalled, the database locked -- the
// choice is to drop something or to block, and blocking is not available. What
// gets dropped is said out loud and counted, because a log that quietly loses
// events is worse than no log: everything derived from it is wrong by an amount
// nobody knows.
type EventWriter struct {
	db      *SQLiteDB
	queue   chan table.HandEvent
	done    chan struct{}
	stopped sync.Once

	dropped atomic.Int64
	written atomic.Int64
}

// NewEventWriter starts the writer. The queue holds a few seconds of play: a
// busy table produces a handful of events a hand, so this is room for a stall,
// not a buffer that would hide one.
func NewEventWriter(db *SQLiteDB) *EventWriter {
	w := &EventWriter{
		db:    db,
		queue: make(chan table.HandEvent, 4096),
		done:  make(chan struct{}),
	}
	go w.run()
	return w
}

// Append queues events. It never blocks and never fails: the caller is a
// capture loop and has nothing useful to do with an error.
func (w *EventWriter) Append(events ...table.HandEvent) {
	if w == nil {
		return
	}
	for _, e := range events {
		select {
		case w.queue <- e:
		default:
			if n := w.dropped.Add(1); n == 1 || n%100 == 0 {
				log.Printf("[EVENTS] queue full, dropped %d event(s) -- the database is not keeping up", n)
			}
		}
	}
}

// Written and Dropped are for the tests and for saying, at shutdown, whether
// anything was lost.
func (w *EventWriter) Written() int64 { return w.written.Load() }
func (w *EventWriter) Dropped() int64 { return w.dropped.Load() }

func (w *EventWriter) run() {
	defer close(w.done)

	// Batched, because one transaction per event would turn a few events a
	// second into a few fsyncs a second for no benefit. Flushed on a timer as
	// well as on a full batch, so a quiet table still lands its events promptly
	// rather than holding them until the next burst.
	const maxBatch = 256
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]table.HandEvent, 0, maxBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.db.AppendEvents(batch); err != nil {
			// Kept, not dropped: the insert ignores duplicates, so retrying the
			// whole batch is safe and costs nothing when most of it landed.
			log.Printf("[EVENTS] write failed, will retry %d event(s): %v", len(batch), err)
			return
		}
		w.written.Add(int64(len(batch)))
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-w.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Close drains what is queued and stops the writer.
func (w *EventWriter) Close() {
	if w == nil {
		return
	}
	w.stopped.Do(func() {
		close(w.queue)
		<-w.done
		if d := w.dropped.Load(); d > 0 {
			log.Printf("[EVENTS] %d event(s) were dropped this session; statistics derived from them are incomplete", d)
		}
	})
}
