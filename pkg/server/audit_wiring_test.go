package server

import (
	"os"
	"path/filepath"
	"testing"

	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
	"poker-game-analyzer/pkg/vision"
)

// Every processed event must reach the decision audit, including the ones that
// produce no recommendation -- a spectator frame with no hole cards is exactly
// the case worth recording.
func TestProcessEvent_WritesToAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	lg, err := audit.NewLogger(path)
	if err != nil {
		t.Fatalf("opening audit log: %v", err)
	}
	defer lg.Close()

	srv := NewServer(storage.NewMemoryCache(), nil, nil)
	srv.SetAuditLogger(lg)

	board, err := table.ParseCards("10c 8s 2c")
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}

	ev := vision.VisionEvent{
		TableID: "t",
		HandState: &table.HandState{
			TableID: "t", Street: table.StreetFlop, Pot: 1000,
			CommunityCards: board,
			Seats:          []table.SeatState{{PlayerID: "v", Stack: 5000, IsActive: true}},
		},
	}

	if _, err := srv.ProcessEvent(ev); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	if got := lg.Written(); got != 1 {
		t.Fatalf("expected 1 audit record, got %d", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit log: %v", err)
	}
	if info.Size() == 0 {
		t.Error("audit log file is empty")
	}
}
