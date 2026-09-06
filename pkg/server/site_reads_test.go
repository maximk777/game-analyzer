package server

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"poker-game-analyzer/pkg/audit"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// A table where we have no history of our own still has to be advised with a
// read: the site's own aggregate arrives with the state, and the decision has to
// be built from it. Before this, an opponent nobody had recorded was a stranger
// -- 0/0/0 in the panel and a hundred-percent range in the model -- while the
// client's own popup, on the same player, showed 27/21/10.
func TestSiteReadsReachTheDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	lg, err := audit.NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	srv := NewServer(storage.NewMemoryCache(), nil, nil)
	srv.SetAuditLogger(lg)

	hero, err := table.ParseCards("Ah Kh")
	if err != nil {
		t.Fatal(err)
	}
	state := &table.HandState{
		HandID: "h1", TableID: "t", Street: table.StreetPreflop,
		Pot: 0.35, CurrentBet: 0.25, BigBlind: 0.1,
		HeroID: "1783560", HeroCards: [2]table.Card{hero[0], hero[1]},
		IsHeroTurn: true, HeroButtons: []string{"fold", "call", "raise"},
		Seats: []table.SeatState{
			{PlayerID: "1783560", PlayerName: "hero", Stack: 10, IsActive: true},
			{PlayerID: "508957", PlayerName: "sammut", Stack: 11, CurrentBet: 0.25, IsActive: true,
				Site: &table.SiteStats{
					VPIP: 0.2669, PFR: 0.2107, ThreeBet: 0.0961,
					FoldToThreeBet: 0.5627, FoldToCBet: 0.3147, Pool: "cash",
				}},
		},
	}

	rec, err := srv.IngestLiveState(state)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("no recommendation on hero's turn")
	}
	if !rec.HasReads {
		t.Error("advice reports no reads while the site's aggregate was on the seat")
	}

	// The audit records the tendencies the decision actually used, so it is
	// where the read can be checked rather than assumed.
	tend := lastSeatTendencies(t, path, "508957")
	if tend == nil {
		t.Fatal("no tendencies recorded for the opponent")
	}
	if v := tend["vpip"]; v < 26.6 || v > 26.8 {
		t.Errorf("vpip used = %v, want ~26.7", v)
	}
	if v := tend["fold_to_cbet"]; v != 0.3147 {
		t.Errorf("fold_to_cbet used = %v, want 0.3147", v)
	}
	if tend["site"] != 1 {
		t.Error("the read does not say it came from the site")
	}
}

// lastSeatTendencies reads the tendencies the audit log recorded for one seat.
func lastSeatTendencies(t *testing.T, path, playerID string) map[string]float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out map[string]float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var rec struct {
			Seats []struct {
				PlayerID   string             `json:"player_id"`
				Tendencies map[string]float64 `json:"tendencies"`
			} `json:"seats"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		for _, s := range rec.Seats {
			if s.PlayerID == playerID && len(s.Tendencies) > 0 {
				out = s.Tendencies
			}
		}
	}
	return out
}
