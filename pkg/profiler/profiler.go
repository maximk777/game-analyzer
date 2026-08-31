package profiler

import (
	"context"
	"math"
	"sync"
	"time"

	"poker-game-analyzer/pkg/llm"
	"poker-game-analyzer/pkg/storage"
	"poker-game-analyzer/pkg/table"
)

// ProfilerOption configures Profiler instance.
type ProfilerOption func(*Profiler)

// WithAnalyzeInterval sets how often (in hands count) to re-profile a player with LLM.
func WithAnalyzeInterval(hands int) ProfilerOption {
	return func(p *Profiler) {
		if hands > 0 {
			p.analyzeInterval = hands
		}
	}
}

// WithDebounceDuration sets the minimum interval between LLM profile runs for a player.
func WithDebounceDuration(d time.Duration) ProfilerOption {
	return func(p *Profiler) {
		p.debounceDur = d
	}
}

// WithWorkerCount sets the number of background worker goroutines.
func WithWorkerCount(n int) ProfilerOption {
	return func(p *Profiler) {
		if n > 0 {
			p.workerCount = n
		}
	}
}

// WithQueueSize sets the capacity of the async task queue.
func WithQueueSize(size int) ProfilerOption {
	return func(p *Profiler) {
		if size > 0 {
			p.queueSize = size
		}
	}
}

type playerAccumulator struct {
	PlayerID              string
	PlayerName            string
	HandsCount            int
	VPIPHands             int
	PFRHands              int
	ThreeBetCount         int
	ThreeBetOpportunities int
	BetsCount             int
	RaisesCount           int
	CallsCount            int

	// Counted fold frequencies. See folds.go for why these exist at all.
	Faced facedCounts
}

func (a *playerAccumulator) toStats() storage.PlayerStats {
	if a.HandsCount == 0 {
		return storage.PlayerStats{
			PlayerID:   a.PlayerID,
			PlayerName: a.PlayerName,
		}
	}

	vpip := (float64(a.VPIPHands) / float64(a.HandsCount)) * 100.0
	pfr := (float64(a.PFRHands) / float64(a.HandsCount)) * 100.0

	var threeBet float64
	if a.ThreeBetOpportunities > 0 {
		threeBet = (float64(a.ThreeBetCount) / float64(a.ThreeBetOpportunities)) * 100.0
	} else {
		threeBet = (float64(a.ThreeBetCount) / float64(a.HandsCount)) * 100.0
	}

	var af float64
	if a.CallsCount > 0 {
		af = float64(a.BetsCount+a.RaisesCount) / float64(a.CallsCount)
	} else {
		af = float64(a.BetsCount + a.RaisesCount)
	}

	st := storage.PlayerStats{
		PlayerID:   a.PlayerID,
		PlayerName: a.PlayerName,
		HandsCount: a.HandsCount,
		VPIP:       math.Round(vpip*10) / 10,
		PFR:        math.Round(pfr*10) / 10,
		ThreeBet:   math.Round(threeBet*10) / 10,
		AF:         math.Round(af*10) / 10,
	}
	// Fold frequencies are fractions, not percentages, because that is the
	// range the advisor validates them against. They are left absent rather
	// than zeroed when the sample is too small: zero means "never folds", which
	// is the most dangerous thing this map can wrongly say.
	if v, ok := foldRate(a.Faced.raiseFolded, a.Faced.raiseFaced); ok {
		st.FoldToRaise, st.FoldToRaiseN = v, a.Faced.raiseFaced
	}
	if v, ok := foldRate(a.Faced.threeBetFold, a.Faced.threeBetFaced); ok {
		st.FoldTo3Bet, st.FoldTo3BetN = v, a.Faced.threeBetFaced
	}
	if v, ok := foldRate(a.Faced.cbetFolded, a.Faced.cbetFaced); ok {
		st.FoldToCBet, st.FoldToCBetN = v, a.Faced.cbetFaced
	}
	if v, ok := foldRate(a.Faced.betFolded, a.Faced.betFaced); ok {
		st.FoldToBet, st.FoldToBetN = v, a.Faced.betFaced
	}
	if v, ok := foldRate(a.Faced.raiseFoldedPost, a.Faced.raiseFacedPost); ok {
		st.FoldToRaisePost, st.FoldToRaisePostN = v, a.Faced.raiseFacedPost
	}
	if v, ok := foldRate(a.Faced.betFlop, a.Faced.betFlopSpots); ok {
		st.BetFreqFlop, st.BetFreqFlopN = v, a.Faced.betFlopSpots
	}
	if v, ok := foldRate(a.Faced.betLate, a.Faced.betLateSpots); ok {
		st.BetFreqLate, st.BetFreqLateN = v, a.Faced.betLateSpots
	}
	return st
}

// Profiler manages statistical accumulation and asynchronous LLM-driven opponent profiling.
type Profiler struct {
	cache           *storage.MemoryCache
	db              *storage.SQLiteDB
	llmClient       llm.Client
	analyzeInterval int
	debounceDur     time.Duration
	workerCount     int
	queueSize       int

	mu              sync.RWMutex
	playerHistories map[string][]table.HandState
	rawStats        map[string]*playerAccumulator
	lastAnalyzedAt  map[string]time.Time

	taskQueue chan string
	wg        sync.WaitGroup
	quit      chan struct{}
	closeOnce sync.Once
	closed    bool

	pendingWg sync.WaitGroup
}

// NewProfiler constructs and starts a new Profiler.
func NewProfiler(cache *storage.MemoryCache, db *storage.SQLiteDB, llmClient llm.Client, opts ...ProfilerOption) *Profiler {
	p := &Profiler{
		cache:           cache,
		db:              db,
		llmClient:       llmClient,
		analyzeInterval: 5,
		debounceDur:     0,
		workerCount:     2,
		queueSize:       200,
		playerHistories: make(map[string][]table.HandState),
		rawStats:        make(map[string]*playerAccumulator),
		lastAnalyzedAt:  make(map[string]time.Time),
		quit:            make(chan struct{}),
	}

	for _, opt := range opts {
		opt(p)
	}

	p.taskQueue = make(chan string, p.queueSize)

	// Start background workers
	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.workerLoop()
	}

	return p
}

func (p *Profiler) workerLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.quit:
			return
		case playerID, ok := <-p.taskQueue:
			if !ok {
				return
			}
			p.processAnalysisTask(playerID)
		}
	}
}

func (p *Profiler) processAnalysisTask(playerID string) {
	defer p.pendingWg.Done()

	if p.llmClient == nil {
		return
	}

	p.mu.RLock()
	accum, exists := p.rawStats[playerID]
	if !exists {
		p.mu.RUnlock()
		return
	}
	stats := accum.toStats()
	history := make([]table.HandState, len(p.playerHistories[playerID]))
	copy(history, p.playerHistories[playerID])
	p.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	profile, err := p.llmClient.AnalyzePlayer(ctx, history, stats)
	if err != nil {
		// Log or handle LLM error gracefully without crashing
		return
	}

	if profile != nil {
		if p.cache != nil {
			p.cache.SetProfile(playerID, profile)
		}
		if p.db != nil {
			_ = p.db.SaveLLMProfile(*profile)
		}
		p.mu.Lock()
		p.lastAnalyzedAt[playerID] = time.Now()
		p.mu.Unlock()
	}
}

// ProcessHandEnd processes a completed hand, updates player statistical accumulators, and triggers async profiling.
func (p *Profiler) ProcessHandEnd(hand table.HandState) {
	if p.db != nil {
		_ = p.db.SaveHandHistory(hand)
	}

	// 1. Scan preflop raise progression
	var preflopRaises int
	var firstRaiserID string
	var secondRaiserID string

	for _, act := range hand.ActionHistory {
		if act.Street == table.StreetPreflop {
			if act.Action == table.ActionRaise || act.Action == table.ActionBet || act.Action == table.ActionAllIn {
				preflopRaises++
				if preflopRaises == 1 {
					firstRaiserID = act.PlayerID
				} else if preflopRaises == 2 {
					secondRaiserID = act.PlayerID
				}
			}
		}
	}

	// 2. Identify participating active players from seats
	type playerHandMetrics struct {
		playerID    string
		playerName  string
		isVPIP      bool
		isPFR       bool
		is3Bet      bool
		had3BetOpp  bool
		betsCount   int
		raisesCount int
		callsCount  int
	}

	participating := make(map[string]*playerHandMetrics)

	for _, seat := range hand.Seats {
		if seat.PlayerID == "" || !seat.IsActive {
			continue
		}
		pName := seat.PlayerName
		if pName == "" {
			pName = seat.PlayerID
		}
		participating[seat.PlayerID] = &playerHandMetrics{
			playerID:   seat.PlayerID,
			playerName: pName,
		}
	}

	// Who was bet at, and what they did about it.
	faced := facedAggression(hand.ActionHistory)

	// Analyze actions per player
	for _, act := range hand.ActionHistory {
		m, ok := participating[act.PlayerID]
		if !ok {
			continue
		}

		if act.Street == table.StreetPreflop {
			if act.Action == table.ActionCall || act.Action == table.ActionBet || act.Action == table.ActionRaise || act.Action == table.ActionAllIn {
				m.isVPIP = true
			}
			if act.Action == table.ActionBet || act.Action == table.ActionRaise || act.Action == table.ActionAllIn {
				m.isPFR = true
			}
			if act.PlayerID == secondRaiserID {
				m.is3Bet = true
			}
			if preflopRaises >= 1 && act.PlayerID != firstRaiserID {
				m.had3BetOpp = true
			}
		} else { // Flop, Turn, River
			if act.Action == table.ActionBet {
				m.betsCount++
			} else if act.Action == table.ActionRaise || act.Action == table.ActionAllIn {
				m.raisesCount++
			} else if act.Action == table.ActionCall {
				m.callsCount++
			}
		}
	}

	// 3. Accumulate stats and update DB/Cache under lock
	var playersToProfile []string

	p.mu.Lock()
	for pID, m := range participating {
		accum, ok := p.rawStats[pID]
		if !ok {
			accum = &playerAccumulator{
				PlayerID:   pID,
				PlayerName: m.playerName,
			}
			p.rawStats[pID] = accum
		}
		accum.PlayerName = m.playerName
		accum.HandsCount++
		if m.isVPIP {
			accum.VPIPHands++
		}
		if m.isPFR {
			accum.PFRHands++
		}
		if m.is3Bet {
			accum.ThreeBetCount++
		}
		if m.had3BetOpp {
			accum.ThreeBetOpportunities++
		}
		accum.BetsCount += m.betsCount
		accum.RaisesCount += m.raisesCount
		accum.CallsCount += m.callsCount
		if f, ok := faced[pID]; ok {
			accum.Faced.raiseFaced += f.raiseFaced
			accum.Faced.raiseFolded += f.raiseFolded
			accum.Faced.threeBetFaced += f.threeBetFaced
			accum.Faced.threeBetFold += f.threeBetFold
			accum.Faced.cbetFaced += f.cbetFaced
			accum.Faced.cbetFolded += f.cbetFolded
			accum.Faced.betFaced += f.betFaced
			accum.Faced.betFolded += f.betFolded
			accum.Faced.raiseFacedPost += f.raiseFacedPost
			accum.Faced.raiseFoldedPost += f.raiseFoldedPost
			accum.Faced.betFlop += f.betFlop
			accum.Faced.betFlopSpots += f.betFlopSpots
			accum.Faced.betLate += f.betLate
			accum.Faced.betLateSpots += f.betLateSpots
		}

		// Keep recent hand history
		p.playerHistories[pID] = append(p.playerHistories[pID], hand)
		if len(p.playerHistories[pID]) > 30 {
			p.playerHistories[pID] = p.playerHistories[pID][1:]
		}

		stats := accum.toStats()
		if p.cache != nil {
			p.cache.SetPlayerStats(pID, &stats)
		}
		if p.db != nil {
			_ = p.db.SavePlayerStats(stats)
		}

		// Check if LLM profiling should be scheduled
		if p.llmClient != nil && !p.closed {
			shouldProfile := false
			if accum.HandsCount == 1 {
				shouldProfile = true
			} else if p.analyzeInterval > 0 && accum.HandsCount%p.analyzeInterval == 0 {
				shouldProfile = true
			}

			if p.debounceDur > 0 {
				if lastTime, exists := p.lastAnalyzedAt[pID]; exists && time.Since(lastTime) < p.debounceDur {
					shouldProfile = false
				}
			}

			if shouldProfile {
				// The debounce clock starts when the analysis is *scheduled*,
				// not when it comes back.
				//
				// Starting it at completion made the rate limit a race: the
				// worker cannot record the timestamp until it has the lock this
				// loop is holding, so a run of hands processed back to back
				// scheduled every one of them and the limit did nothing. The
				// test for it passed or failed on how long the loop took, which
				// is how a change to an unrelated statistic came to break it.
				// Scheduling is also the thing worth limiting: an analysis
				// already in flight is a cost already paid.
				p.lastAnalyzedAt[pID] = time.Now()
				playersToProfile = append(playersToProfile, pID)
			}
		}
	}
	p.mu.Unlock()

	// 4. Enqueue LLM profiling tasks
	for _, pID := range playersToProfile {
		p.pendingWg.Add(1)
		select {
		case p.taskQueue <- pID:
		default:
			// Queue full - don't block real-time game flow
			p.pendingWg.Done()
		}
	}
}

// GetStats returns current PlayerStats for a player from memory, cache, or database.
func (p *Profiler) GetStats(playerID string) *storage.PlayerStats {
	p.mu.RLock()
	accum, ok := p.rawStats[playerID]
	if ok {
		s := accum.toStats()
		p.mu.RUnlock()
		return &s
	}
	p.mu.RUnlock()

	if p.cache != nil {
		if s := p.cache.GetPlayerStats(playerID); s != nil {
			return s
		}
	}
	if p.db != nil {
		if s, err := p.db.GetPlayerStats(playerID); err == nil && s != nil {
			return s
		}
	}
	return nil
}

// GetProfile returns current LLMProfile for a player from cache or database.
func (p *Profiler) GetProfile(playerID string) *storage.LLMProfile {
	if p.cache != nil {
		if prof := p.cache.GetProfile(playerID); prof != nil {
			return prof
		}
	}
	if p.db != nil {
		if prof, err := p.db.GetLLMProfile(playerID); err == nil && prof != nil {
			return prof
		}
	}
	return nil
}

// GetPlayerTendencies returns a key-value map of tendencies for use by advisor and decision models.
func (p *Profiler) GetPlayerTendencies(playerID string) map[string]float64 {
	tendencies := make(map[string]float64)

	stats := p.GetStats(playerID)
	if stats != nil {
		tendencies["vpip"] = stats.VPIP
		tendencies["pfr"] = stats.PFR
		tendencies["three_bet"] = stats.ThreeBet
		tendencies["af"] = stats.AF
		tendencies["hands_count"] = float64(stats.HandsCount)

		// Counted fold frequencies, and the sample behind each. The advisor
		// weights a read by how much of it there is, and until now the only
		// count it could weight by was hands played -- which says nothing about
		// how often anybody bet at this player.
		if stats.FoldToRaiseN > 0 {
			tendencies["fold_to_raise"] = stats.FoldToRaise
			tendencies["fold_to_raise_n"] = float64(stats.FoldToRaiseN)
		}
		if stats.FoldTo3BetN > 0 {
			tendencies["fold_to_3bet"] = stats.FoldTo3Bet
			tendencies["fold_to_3bet_n"] = float64(stats.FoldTo3BetN)
		}
		if stats.FoldToCBetN > 0 {
			tendencies["fold_to_cbet"] = stats.FoldToCBet
			tendencies["fold_to_cbet_n"] = float64(stats.FoldToCBetN)
		}
		if stats.FoldToBetN > 0 {
			tendencies["fold_to_bet"] = stats.FoldToBet
			tendencies["fold_to_bet_n"] = float64(stats.FoldToBetN)
		}
		if stats.FoldToRaisePostN > 0 {
			tendencies["fold_to_raise_post"] = stats.FoldToRaisePost
			tendencies["fold_to_raise_post_n"] = float64(stats.FoldToRaisePostN)
		}
		if stats.BetFreqFlopN > 0 {
			tendencies["bet_freq_flop"] = stats.BetFreqFlop
			tendencies["bet_freq_flop_n"] = float64(stats.BetFreqFlopN)
		}
		if stats.BetFreqLateN > 0 {
			tendencies["bet_freq_late"] = stats.BetFreqLate
			tendencies["bet_freq_late_n"] = float64(stats.BetFreqLateN)
		}
	}

	// The language model fills in only what was not counted. A counted
	// frequency is an observation; a modelled one is an opinion formed from
	// summary statistics, and where both exist the observation wins.
	profile := p.GetProfile(playerID)
	if profile != nil {
		tendencies["bluff_frequency"] = profile.BluffFrequency
		if _, ok := tendencies["fold_to_3bet"]; !ok {
			tendencies["fold_to_3bet"] = profile.FoldTo3Bet
			tendencies["modelled"] = 1
		}
		if _, ok := tendencies["fold_to_cbet"]; !ok {
			tendencies["fold_to_cbet"] = profile.FoldToCBet
			tendencies["modelled"] = 1
		}
	}

	return tendencies
}

// Flush waits for all in-flight profiling tasks to finish.
func (p *Profiler) Flush() {
	p.pendingWg.Wait()
}

// Close gracefully stops all worker goroutines.
func (p *Profiler) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		close(p.quit)
		p.wg.Wait()
	})
}
