// Replays a recorded live session through the stabiliser, and reports what the
// profiler would have seen.
//
// bin/logs/live_session.jsonl is one raw frame per line, exactly as the Swift
// reader produced it -- before any smoothing, hand-lifecycle or statistics. It
// is the only record of what the table actually looked like, and it is what
// makes a question like "why is 3-bet always zero" answerable without a live
// table in front of you.
//
//	go run ./cmd/replay bin/logs/live_session.jsonl
//	go run ./cmd/replay bin/logs/live_session.jsonl --hands   # per-hand detail
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"poker-game-analyzer/pkg/profiler"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: replay <live_session.jsonl> [--hands]")
		os.Exit(2)
	}
	detail := false
	dbPath := ""
	for i, a := range os.Args[2:] {
		if a == "--hands" {
			detail = true
		}
		if a == "--db" && i+3 <= len(os.Args)-1 {
			dbPath = os.Args[i+3]
		}
	}

	// Writing the replay into a database is how the event log is checked
	// against something real: the same frames, through the same stabiliser,
	// into the same schema the live agent uses.
	var writer *storage.EventWriter
	var store *storage.SQLiteDB
	if dbPath != "" {
		var err error
		store, err = storage.NewSQLiteDB(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opening %s: %v\n", dbPath, err)
			os.Exit(1)
		}
		defer store.Close()
		writer = storage.NewEventWriter(store)
		defer writer.Close()
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening session log: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	stab := table.NewStateStabilizer()
	// Named after the recording, not the clock: replaying the same file twice
	// has to re-derive the same hands rather than mint a second set of them.
	if info, err := os.Stat(os.Args[1]); err == nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", info.Name(), info.Size(), info.ModTime().UnixNano())))
		stab.SetSessionID("replay-" + hex.EncodeToString(sum[:6]))
	}
	var completed []table.HandState
	// Frames observed since the last completed hand, so a hand with no actions
	// can be told apart from a hand that was simply never seen.
	framesThisHand := 0
	var framesPerHand []int
	// Badges present in the raw frames of the hand, before the stabiliser turns
	// them into actions. This separates "vision saw nothing" from "vision saw
	// it and the action stream dropped it".
	rawBadges := map[string]int{}
	var badgesPerHand []map[string]int
	// Distinct complete hole-card readings seen in the raw frames of one
	// stabilised hand. More than one means two real hands were merged into it:
	// the transition between them was not recognised, which is what losing the
	// frames at the start of a hand would look like from here.
	rawHero := map[string]int{}
	var heroPerHand []map[string]int

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
	frames, bad := 0, 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var st table.HandState
		if err := json.Unmarshal(line, &st); err != nil {
			bad++
			continue
		}
		frames++
		framesThisHand++
		if st.HeroCards[0].Known() && st.HeroCards[1].Known() {
			a, b := st.HeroCards[0].String(), st.HeroCards[1].String()
			if a > b {
				a, b = b, a
			}
			rawHero[a+" "+b]++
		}
		for _, seat := range st.Seats {
			if seat.PlayerID != "" && seat.LastAction != "" {
				// Keyed by the street the raw frame reported, because a "bet"
				// badge on the flop is not preflop aggression -- counting them
				// together was the mistake that made this look like a much
				// bigger loss than it is.
				rawBadges[string(st.Street)+":"+seat.PlayerID+":"+seat.LastAction]++
			}
		}
		stab.Stabilize(&st)
		if writer != nil {
			writer.Append(stab.TakeEvents()...)
		} else {
			_ = stab.TakeEvents()
		}
		if done := stab.TakeCompletedHand(); done != nil {
			completed = append(completed, *done)
			framesPerHand = append(framesPerHand, framesThisHand)
			badgesPerHand = append(badgesPerHand, rawBadges)
			heroPerHand = append(heroPerHand, rawHero)
			framesThisHand = 0
			rawBadges = map[string]int{}
			rawHero = map[string]int{}
		}
	}

	fmt.Printf("frames %d (undecodable %d)  hands completed %d\n\n", frames, bad, len(completed))

	if writer != nil {
		writer.Close()

		// Fold the events into counters the same way the agent will, so the
		// pipeline is exercised end to end rather than in halves.
		cursor := profiler.NewStatsCursor(store)
		counted := 0
		for {
			n, err := cursor.Run(2000)
			if err != nil {
				fmt.Fprintf(os.Stderr, "stats cursor: %v\n", err)
				break
			}
			if n == 0 {
				break
			}
			counted += n
		}
		fmt.Printf("stats cursor folded %d hands\n", counted)

		n, err := store.EventCount()
		if err != nil {
			fmt.Fprintf(os.Stderr, "counting events: %v\n", err)
		} else {
			fmt.Printf("events written %d (queued %d, dropped %d) -> %s\n\n",
				n, writer.Written(), writer.Dropped(), dbPath)
		}
	}

	// The same reading of the action stream the profiler makes, reported rather
	// than accumulated: preflop raises in order, and who made the second one.
	var withActions, withPreflopRaise, withThreeBet int
	var profilerThreeBet, lostToFilter int
	streetCounts := map[table.Street]int{}
	actionCounts := map[table.ActionType]int{}

	for i, h := range completed {
		var raisers []string
		for _, a := range h.ActionHistory {
			streetCounts[a.Street]++
			actionCounts[a.Action]++
			if a.Street != table.StreetPreflop {
				continue
			}
			if a.Action == table.ActionRaise || a.Action == table.ActionBet || a.Action == table.ActionAllIn {
				raisers = append(raisers, a.PlayerID)
			}
		}
		if len(h.ActionHistory) > 0 {
			withActions++
		}
		if len(raisers) >= 1 {
			withPreflopRaise++
		}
		if len(raisers) >= 2 {
			withThreeBet++
		}
		// What the profiler would actually count. It only looks at seats that
		// are still IsActive at the end of the hand, so a raiser who folded
		// later is invisible to it however clearly the action stream recorded
		// the raise.
		active := map[string]bool{}
		for _, seat := range h.Seats {
			if seat.PlayerID != "" && seat.IsActive {
				active[seat.PlayerID] = true
			}
		}
		var counted []string
		for _, r := range raisers {
			if active[r] {
				counted = append(counted, r)
			}
		}
		if len(counted) >= 2 {
			profilerThreeBet++
		}
		if len(raisers) >= 2 && len(counted) < 2 {
			lostToFilter++
		}
		if detail {
			fmt.Printf("  hand %3d %-44s actions=%-3d raisers=%v counted=%v seats=%d active=%d\n",
				i+1, h.HandID, len(h.ActionHistory), raisers, counted, len(h.Seats), len(active))
		}
	}
	if detail {
		fmt.Println()
	}

	fmt.Printf("hands with any action recorded : %d / %d\n", withActions, len(completed))
	fmt.Printf("hands with a preflop raise     : %d\n", withPreflopRaise)
	fmt.Printf("hands with a second raiser (3b): %d\n", withThreeBet)
	fmt.Printf("  of those, counted by profiler: %d\n", profilerThreeBet)
	fmt.Printf("  lost to the IsActive filter  : %d\n\n", lostToFilter)

	// Where preflop aggression is lost. A raise badge present in the raw frames
	// but absent from the action stream means the badge was read and the stream
	// dropped it; a hand with no raise badge at all means vision never saw one.
	var rawHadRaise, streamHadRaise, rawOnly int
	for i, h := range completed {
		raw := false
		if i < len(badgesPerHand) {
			for k := range badgesPerHand[i] {
				if !strings.HasPrefix(k, string(table.StreetPreflop)+":") {
					continue
				}
				if strings.HasSuffix(k, ":raise") || strings.HasSuffix(k, ":bet") {
					raw = true
				}
			}
		}
		stream := false
		for _, a := range h.ActionHistory {
			if a.Street != table.StreetPreflop {
				continue
			}
			if a.Action == table.ActionRaise || a.Action == table.ActionBet || a.Action == table.ActionAllIn {
				stream = true
			}
		}
		if raw {
			rawHadRaise++
		}
		if stream {
			streamHadRaise++
		}
		if raw && !stream {
			rawOnly++
		}
	}
	fmt.Printf("hands where vision saw a preflop raise    : %d\n", rawHadRaise)
	fmt.Printf("hands where the action stream kept it    : %d\n", streamHadRaise)
	fmt.Printf("hands where the badge was seen and lost  : %d\n\n", rawOnly)

	// Hands that recorded nothing: either they went by in a couple of frames,
	// or the badges were on screen and were not turned into actions.
	var silentShort, silentLong int
	for i, h := range completed {
		if len(h.ActionHistory) > 0 {
			continue
		}
		if i < len(framesPerHand) && framesPerHand[i] >= 10 {
			silentLong++
			if silentLong <= 4 && i < len(badgesPerHand) {
				var seen []string
				for k := range badgesPerHand[i] {
					seen = append(seen, k)
				}
				sort.Strings(seen)
				fmt.Printf("  silent hand %s: %d frames, %d raw badge states seen\n",
					h.HandID, framesPerHand[i], len(seen))
				for _, k := range seen {
					fmt.Printf("      %-32s in %d frames\n", k, badgesPerHand[i][k])
				}
			}
		} else {
			silentShort++
		}
	}
	fmt.Printf("hands recording no action at all: %d\n", silentShort+silentLong)
	fmt.Printf("  seen for fewer than 10 frames : %d\n", silentShort)
	fmt.Printf("  seen for 10 frames or more    : %d\n\n", silentLong)

	// Health of each completed hand. If losing the frames at the start of a
	// hand leaves it unparseable for the rest of its life, it shows up here as
	// a hand that lived for many frames and never resolved.
	var noHero, noBoard, shortLived int
	var brokenLong []string
	for i, h := range completed {
		frames := 0
		if i < len(framesPerHand) {
			frames = framesPerHand[i]
		}
		heroKnown := h.HeroCards[0].Known() && h.HeroCards[1].Known()
		if !heroKnown {
			noHero++
		}
		if len(h.CommunityCards) == 0 {
			noBoard++
		}
		if frames < 5 {
			shortLived++
		}
		// Lived long enough to be read, and was not.
		if frames >= 15 && !heroKnown {
			brokenLong = append(brokenLong, fmt.Sprintf("%s frames=%d board=%d actions=%d street=%s",
				h.HandID, frames, len(h.CommunityCards), len(h.ActionHistory), h.Street))
		}
	}
	// Two real hands inside one stabilised hand, and hole cards that the raw
	// frames carried but the merged state did not.
	var merged, heroLost int
	for i, h := range completed {
		if i >= len(heroPerHand) {
			break
		}
		seen := heroPerHand[i]
		if len(seen) > 1 {
			merged++
			var list []string
			for k, n := range seen {
				list = append(list, fmt.Sprintf("%s x%d", k, n))
			}
			sort.Strings(list)
			fmt.Printf("  merged hand %s: raw frames carried %d different holdings -- %s\n",
				h.HandID, len(seen), strings.Join(list, ", "))
		}
		if len(seen) > 0 && !(h.HeroCards[0].Known() && h.HeroCards[1].Known()) {
			heroLost++
		}
	}
	fmt.Printf("\nhands holding two real hands   : %d / %d\n", merged, len(completed))
	fmt.Printf("hands where raw had hero cards and the merged state did not: %d\n", heroLost)
	fmt.Printf("hands with no hero cards       : %d / %d\n", noHero, len(completed))
	fmt.Printf("hands with no board at all     : %d\n", noBoard)
	fmt.Printf("hands seen for under 5 frames  : %d\n", shortLived)
	fmt.Printf("hands seen 15+ frames, hero unread: %d\n", len(brokenLong))
	for _, b := range brokenLong {
		if len(b) > 0 {
			fmt.Printf("    %s\n", b)
		}
	}
	fmt.Println()

	fmt.Println("actions by street:")
	var streets []string
	for s := range streetCounts {
		streets = append(streets, string(s))
	}
	sort.Strings(streets)
	for _, s := range streets {
		fmt.Printf("  %-10s %d\n", s, streetCounts[table.Street(s)])
	}
	fmt.Println("\nactions by kind:")
	var kinds []string
	for a := range actionCounts {
		kinds = append(kinds, string(a))
	}
	sort.Strings(kinds)
	for _, a := range kinds {
		fmt.Printf("  %-10s %d\n", a, actionCounts[table.ActionType(a)])
	}
}
