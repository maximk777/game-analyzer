# Real-Time Poker Game Analyzer & Assistant (RTA) Engine

**Date:** 2026-08-30  
**Status:** Approved  
**Language/Stack:** Go (Golang) 1.22+, SQLite, WebSockets, HTML5/CSS/JS Overlay  

---

## 1. Executive Summary

The **Real-Time Poker Game Analyzer** is an extensible, high-performance RTA (Real-Time Assistance) and game modeling engine designed for No-Limit Texas Hold'em. It acts as an intelligent co-pilot from the single-player (Hero) perspective—relying only on information visible to a legitimate player (Hero hole cards, community board cards, player stacks, bets, and actions)—while inferring opponent ranges, calculating win equity via Monte Carlo simulations in under 5 milliseconds, and computing mathematically optimal Expected Value (EV) decisions for Fold, Check, Call, Bet, and Raise.

Additionally, the system features an asynchronous LLM-powered Opponent Behavioral Profiler that analyzes accumulated hand logs to construct psychological profiles, bluff indices, and exploitative tendency coefficients (e.g., loose-aggressive, tight-passive, tilt-prone), which dynamically modulate opponent range models and fold equity in real-time.

---

## 2. System Architecture

```
                                 ┌────────────────────────────────────────┐
                                 │   CoinPoker / Game Client Window       │
                                 └──────────────────┬─────────────────────┘
                                                    │ (Screen Capture / BitBlt / SCK)
                                                    ▼
                                 ┌────────────────────────────────────────┐
                                 │   Module 1: Vision Ingestion & OCR     │
                                 │   - ROI Grid Normalization (6-max)     │
                                 │   - Template Matching (Cards / Suits)  │
                                 │   - Text/Number Extraction (Stacks/Pot)│
                                 │   - Frame State Diffing & Event Emitter│
                                 └──────────────────┬─────────────────────┘
                                                    │
                                                    │ Game Events (Stream)
                                                    ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                               Go Real-Time Core Engine                                 │
│                                                                                        │
│  ┌──────────────────────────────┐              ┌────────────────────────────────────┐  │
│  │ Module 2: Table & State Mgr  │              │ Module 3: Fast Math & Decision Eng │  │
│  │ - REST & WebSocket Hub       │─────────────▶│ - Bitwise Hand Evaluator (<50ns)   │  │
│  │ - Table/Seat Registry        │              │ - Monte Carlo Equity Sim (<3ms)    │  │
│  │ - Hero Perspective Isolation │              │ - Pot Odds & Fold Equity Calculator│  │
│  └──────────────┬───────────────┘              │ - EV Maximizer & Bet Sizer         │  │
│                 │                              └─────────────────┬──────────────────┘  │
│                 │                                                │                     │
│                 │ Hand End Events                                │ Dynamic Range       │
│                 ▼                                                │ Adjustments         │
│  ┌──────────────────────────────┐              ┌─────────────────┴──────────────────┐  │
│  │ Module 4: Opponent Profiler  │              │ Module 5: Storage & Cache          │  │
│  │ - Fast Stats (VPIP/PFR/3Bet) │─────────────▶│ - In-Memory Active Tables/Profiles │  │
│  │ - Async LLM Profiler Worker  │              │ - SQLite Persistence (Hands/Stats) │  │
│  └──────────────────────────────┘              └────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────────┬────────────────────┘
                                                                    │ WebSocket Broadcast
                                                                    ▼
                                                 ┌────────────────────────────────────────┐
                                                 │ Module 6: Web Dashboard & RTA Overlay  │
                                                 │ - Live HUD on top of poker table       │
                                                 │ - Real-time Action & Sizing Bar        │
                                                 │ - Opponent archetype badges & stats    │
                                                 └────────────────────────────────────────┘
```

---

## 3. Core Modules & Subsystems

### Module 1: Vision Ingestion & OCR Engine (`pkg/vision`)
* **Capture Layer:** Platform-native frame grabber (Quartz / ScreenCaptureKit on macOS, BitBlt on Windows/Linux) capturing target window at 5–10 FPS.
* **ROI (Region of Interest) Grid:** Configurable relative coordinate grid normalized (0.0–1.0) for 6-max tables (e.g. CoinPoker layout):
  * Hero Hole Cards (2 slots at bottom center);
  * Community Board Cards (5 slots at table center);
  * 6 Player Seats: Avatar, Nameplate, Chip Stack, Active Action Badge (SB, BB, D, FOLD, CALL, RAISE), Bet Chip Amount;
  * Pot Amount and Action Buttons / Timer Bar.
* **Card & Rank Recognition:**
  * High-accuracy Perceptual Hash / Grayscale Template Matching for 13 ranks (`2`-`A`) and 4 suits (`s`, `h`, `d`, `c`).
  * Instant 100% deterministic recognition without neural network overhead (<0.5ms per card).
* **Number/Text Parsing:** Fast character grid OCR for amounts (e.g., `24.65`, `Pot 0.85`, `18.23`).
* **State Diffing & Event Generator:** Detects discrete state transitions and fires structured events: `HandStarted`, `CardDealt`, `PlayerActed`, `HeroTurn`, `HandEnded`.

### Module 2: Table Management & Event Pipeline (`pkg/table`, `pkg/server`)
* **Hero Perspective Isolation:** Enforces strict game realism where only Hero cards are known in advance. Opponents' cards remain hidden until explicit showdown events.
* **REST API:**
  * `POST /api/v1/tables` — create/initialize table configuration (blinds, limits, seats).
  * `GET /api/v1/tables/{id}` — fetch snapshot of table, seats, and current hand state.
  * `GET /api/v1/players/{id}/profile` — fetch statistical and LLM profile for a player.
* **WebSocket Streaming (`/ws/tables/{id}?hero_id={heroId}`):**
  * Bidirectional stream for receiving live vision/simulation events and broadcasting sub-5ms recommendations to the HUD overlay.

### Module 3: Fast Poker Math & Decision Engine (`pkg/evaluator`, `pkg/equity`, `pkg/advisor`)
* **Bitwise Hand Evaluator:**
  * Lookup-Table / Two-Plus-Two 7-card hand evaluation algorithm completing in < 50 nanoseconds per hand.
* **Monte Carlo Equity Simulator:**
  * Simulates 10,000+ random board/opponent runouts in < 3ms against known dead cards and opponent range distributions.
  * Adjusts opponent ranges based on their archetype (e.g., Nit range: top 12%, Loose-Passive: top 45%).
* **Expected Value (EV) & Action Advisor:**
  * **Pot Odds:** $PO = \frac{to\_call}{pot + to\_call}$
  * **Expected Value:**
    * $EV(\text{Fold}) = 0$
    * $EV(\text{Call}) = Equity \times (Pot + to\_call) - to\_call$
    * $EV(\text{Raise}) = P_{fold} \times Pot + (1 - P_{fold}) \times (Equity \times (Pot + 2 \times Raise) - Raise)$
  * Generates concrete recommendation: Action (`Fold`, `Check`, `Call`, `Bet`, `Raise`, `All-In`), Bet Sizing (Min, 33% pot, 66% pot, Pot, All-in), and clear tactical reasoning.

### Module 4: Player Profiler & Async LLM Worker (`pkg/profiler`, `pkg/llm`)
* **Real-time Statistical Accumulator:**
  * $VPIP = \frac{\text{Voluntarily Put $ in Pot}}{\text{Hands}}$, $PFR = \frac{\text{Preflop Raise}}{\text{Hands}}$, $AF = \frac{\text{Bets} + \text{Raises}}{\text{Calls}}$, $3Bet\%$.
* **Async Background LLM Worker:**
  * Queues completed hand histories to LLM (OpenAI / Claude / Gemini / Ollama local).
  * Generates structured JSON profile:
    ```json
    {
      "player_name": "mamayazareyzil",
      "archetype": "loose_aggressive",
      "bluff_frequency": 0.35,
      "fold_to_3bet": 0.25,
      "fold_to_cbet": 0.40,
      "tilt_risk": 0.15,
      "exploits": [
        "Defends blinds excessively against standard raises",
        "Passive on river unless holding two pair or better"
      ],
      "notes": "Aggressive preflop regular, gives up on turn when checked to"
    }
    ```
* **Coefficients Sync:** Writes updated traits to In-Memory Cache and SQLite for immediate consumption by Module 3.

### Module 5: Storage Layer (`pkg/storage`)
* **In-Memory Cache:** Thread-safe state maps (`sync.RWMutex`) for active tables, hands, and cached player coefficients.
* **SQLite Database:**
  * `tables`: table configurations and historical tracking;
  * `players` & `player_stats`: aggregated statistical metrics;
  * `player_profiles`: LLM archetypes, exploitative coefficients, and tactical notes;
  * `hand_histories`: full structured JSON logs of every played hand for simulation replays and offline model training.

### Module 6: Web Dashboard & RTA Overlay (`web/`)
* Lightweight HTML/Canvas overlay with WebSocket connection.
* Displays:
  1. Hero Hand Equity & Pot Odds bar;
  2. Recommended Action with primary and alternative sizing;
  3. Tactical reasoning tooltip;
  4. Table HUD: badges over opponent avatars with archetype (TAG, LAG, Fish, Nit, Whale) and bluff tendency.

---

## 4. Data Models & API Specifications

### Hand State Model (Go struct)
```go
type HandState struct {
    HandID         string           `json:"hand_id"`
    TableID        string           `json:"table_id"`
    Street         Street           `json:"street"` // Preflop, Flop, Turn, River, Showdown
    Pot            float64          `json:"pot"`
    CurrentBet     float64          `json:"current_bet"`
    MinRaise       float64          `json:"min_raise"`
    CommunityCards []Card           `json:"community_cards"`
    HeroID         string           `json:"hero_id"`
    HeroCards      [2]Card          `json:"hero_cards"`
    Seats          []SeatState      `json:"seats"`
    ActionHistory  []ActionRecord   `json:"action_history"`
}
```

### Recommendation Output (WebSocket message)
```json
{
  "type": "recommendation",
  "data": {
    "hand_id": "hand-20260830-001",
    "street": "preflop",
    "hero_cards": ["Ac", "Kh"],
    "equity": 0.674,
    "pot_odds": 0.250,
    "ev_actions": [
      {"action": "fold", "ev": 0.0, "recommended": false},
      {"action": "call", "amount": 0.5, "ev": 0.42, "recommended": false},
      {"action": "raise", "amount": 1.75, "ev": 1.15, "recommended": true, "sizing_label": "3.5x"}
    ],
    "primary_action": "raise",
    "recommended_amount": 1.75,
    "reasoning": "High preflop equity (67.4%) vs LAG opener (mamayazareyzil). 3-bet to 1.75 for value and isolation.",
    "opponent_summaries": [
      {"player_name": "mamayazareyzil", "archetype": "loose_aggressive", "vpip": 38.2, "pfr": 29.1, "bluff_index": 0.42}
    ]
  }
}
```

---

## 5. Testing & Verification Plan

1. **Unit Tests (`pkg/evaluator`):** Test hand ranking accuracy against all 7,462 unique equivalence classes (High Card up to Royal Flush).
2. **Monte Carlo Benchmarks (`pkg/equity`):** Verify 10,000-run equity simulation runs in $< 3$ ms with $< 0.5\%$ variance against known analytical equity tables.
3. **Vision Matcher Tests (`pkg/vision`):** Feed reference screenshots (like CoinPoker 6-max sample) and assert 100% extraction accuracy for cards, stacks, pot, and buttons.
4. **End-to-End Synthetic Replay:** Run a synthetic game generator stream into the WebSocket engine and verify real-time recommendation delivery and SQLite persistence.

---

## 6. Project Layout

```
game-analyzer/
├── cmd/
│   ├── server/           # Main RTA server entrypoint (HTTP/WS)
│   ├── vision-test/      # CLI tool to test vision parsing on screenshots
│   └── simulator/        # Random game generator for offline testing
├── pkg/
│   ├── advisor/          # EV & Action recommendation logic
│   ├── equity/           # Monte Carlo simulator & range tables
│   ├── evaluator/        # High-speed bitwise hand evaluator
│   ├── llm/              # LLM client & prompt templates for profiling
│   ├── profiler/         # Statistical tracker & opponent profiler
│   ├── storage/          # SQLite migrations & queries, In-memory cache
│   ├── table/            # Poker table domain rules & state machine
│   └── vision/           # Screen capture, ROI matcher, card OCR
├── web/                  # HTML5/JS Live Dashboard and HUD Overlay
├── testdata/             # Sample table screenshots and hand logs
├── go.mod
└── docs/superpowers/specs/
```
