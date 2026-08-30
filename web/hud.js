/**
 * POKER RTA HUD // Always-On-Top Floating Widget Controller
 * Real-Time WebSocket Synchronization, GTO Sizing Matrix & Opponent Profiler
 */

(function () {
    "use strict";

    // Application State
    const urlParams = new URLSearchParams(window.location.search);
    const state = {
        tableId: urlParams.get("table_id") || "coinpoker-live",
        ws: null,
        reconnectTimer: null,
        soundEnabled: true,
        currentHand: null,
        currentAdvice: null,
        playerProfiles: new Map(), // playerID -> profile
        audioCtx: null,
    };

    // DOM Elements
    const elements = {
        hudWidget: document.getElementById("hudWidget"),
        hudStatusDot: document.getElementById("hudStatusDot"),
        hudStatusText: document.getElementById("hudStatusText"),
        hudStreetBadge: document.getElementById("hudStreetBadge"),
        hudPotBadge: document.getElementById("hudPotBadge"),
        btnToggleStats: document.getElementById("btnToggleStats"),
        btnToggleSound: document.getElementById("btnToggleSound"),
        btnHudSettings: document.getElementById("btnHudSettings"),
        hudHoleCards: document.getElementById("hudHoleCards"),
        hudHandRank: document.getElementById("hudHandRank"),
        hudCommunityMini: document.getElementById("hudCommunityMini"),
        hudActionBanner: document.getElementById("hudActionBanner"),
        hudActionType: document.getElementById("hudActionType"),
        hudActionAmount: document.getElementById("hudActionAmount"),
        hudEvBadge: document.getElementById("hudEvBadge"),
        hudEquityVal: document.getElementById("hudEquityVal"),
        hudEquityBar: document.getElementById("hudEquityBar"),
        hudPotOddsVal: document.getElementById("hudPotOddsVal"),
        hudPotOddsBar: document.getElementById("hudPotOddsBar"),
        hudVerdictPill: document.getElementById("hudVerdictPill"),
        hudVerdictIcon: document.getElementById("hudVerdictIcon"),
        hudVerdictText: document.getElementById("hudVerdictText"),
        sizeAmtMin: document.getElementById("sizeAmtMin"),
        sizeAmt25x: document.getElementById("sizeAmt25x"),
        sizeAmt33: document.getElementById("sizeAmt33"),
        sizeAmt66: document.getElementById("sizeAmt66"),
        sizeAmtPot: document.getElementById("sizeAmtPot"),
        sizeAmtAllIn: document.getElementById("sizeAmtAllIn"),
        hudReasoningText: document.getElementById("hudReasoningText"),
        hudOpponentsDrawer: document.getElementById("hudOpponentsDrawer"),
        tabOpponents: document.getElementById("tabOpponents"),
        tabActions: document.getElementById("tabActions"),
        actionCount: document.getElementById("actionCount"),
        paneOpponents: document.getElementById("paneOpponents"),
        paneActions: document.getElementById("paneActions"),
        hudOpponentsList: document.getElementById("hudOpponentsList"),
        hudActionsList: document.getElementById("hudActionsList"),
        hudSettingsModal: document.getElementById("hudSettingsModal"),
        btnCloseSettings: document.getElementById("btnCloseSettings"),
        inputTableId: document.getElementById("inputTableId"),
        btnReconnect: document.getElementById("btnReconnect"),
        sliderOpacity: document.getElementById("sliderOpacity"),
        
        // Simulation buttons
        simBtnNewHand: document.getElementById("simBtnNewHand"),
        simBtnFlop: document.getElementById("simBtnFlop"),
        simBtnTurn: document.getElementById("simBtnTurn"),
        simBtnRiver: document.getElementById("simBtnRiver"),
        simBtnBet: document.getElementById("simBtnBet"),
        simBtnHeroTurn: document.getElementById("simBtnHeroTurn"),
        simBtnEnd: document.getElementById("simBtnEnd"),
    };

    // Suit Mappings
    const SUIT_SYMBOLS = {
        0: "♠", 1: "♥", 2: "♦", 3: "♣",
        "s": "♠", "h": "♥", "d": "♦", "c": "♣",
        "♠": "♠", "♥": "♥", "♦": "♦", "♣": "♣"
    };

    const SUIT_CLASSES = {
        0: "suit-s", 1: "suit-h", 2: "suit-d", 3: "suit-c",
        "s": "suit-s", "h": "suit-h", "d": "suit-d", "c": "suit-c",
        "♠": "suit-s", "♥": "suit-h", "♦": "suit-d", "♣": "suit-c"
    };

    const RANK_LABELS = {
        2: "2", 3: "3", 4: "4", 5: "5", 6: "6", 7: "7", 8: "8", 9: "9",
        10: "T", 11: "J", 12: "Q", 13: "K", 14: "A",
        "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9",
        "T": "T", "J": "J", "Q": "Q", "K": "K", "A": "A", "t": "T", "j": "J", "q": "Q", "k": "K", "a": "A"
    };

    function parseCardData(card) {
        if (!card) return null;
        if (typeof card === "string") {
            if (card.length < 2) return null;
            const r = card.slice(0, -1).toUpperCase();
            const s = card.slice(-1).toLowerCase();
            return {
                rank: RANK_LABELS[r] || r,
                suit: s,
                suitClass: SUIT_CLASSES[s] || "suit-s",
                symbol: SUIT_SYMBOLS[s] || "♠"
            };
        }
        if (typeof card === "object") {
            const r = RANK_LABELS[card.rank] || (card.rank > 0 ? String(card.rank) : "?");
            const s = card.suit;
            return {
                rank: r,
                suit: s,
                suitClass: SUIT_CLASSES[s] || "suit-s",
                symbol: SUIT_SYMBOLS[s] || "♠"
            };
        }
        return null;
    }

    function formatMoney(amt) {
        if (typeof amt !== "number" || isNaN(amt)) return "$0.00";
        return `$${amt.toFixed(2)}`;
    }

    // Play Synth Turn Chime
    function playTurnChime() {
        if (!state.soundEnabled) return;
        try {
            if (!state.audioCtx) {
                state.audioCtx = new (window.AudioContext || window.webkitAudioContext)();
            }
            if (state.audioCtx.state === "suspended") {
                state.audioCtx.resume();
            }
            const osc = state.audioCtx.createOscillator();
            const gain = state.audioCtx.createGain();
            osc.type = "sine";
            osc.frequency.setValueAtTime(587.33, state.audioCtx.currentTime); // D5
            osc.frequency.exponentialRampToValueAtTime(880, state.audioCtx.currentTime + 0.15); // A5
            gain.gain.setValueAtTime(0.2, state.audioCtx.currentTime);
            gain.gain.exponentialRampToValueAtTime(0.01, state.audioCtx.currentTime + 0.25);
            osc.connect(gain);
            gain.connect(state.audioCtx.destination);
            osc.start();
            osc.stop(state.audioCtx.currentTime + 0.25);
        } catch (e) {
            // AudioContext not allowed before user interaction
        }
    }

    // Hand Evaluation Heuristic for HUD Label
    function determineMadeHandLabel(heroCards, boardCards) {
        if (!heroCards || heroCards.length < 2 || !heroCards[0] || heroCards[0].rank === "?") {
            if (boardCards && boardCards.length > 0) {
                return `Board: ${boardCards.map(c => c.rank + c.symbol).join(" ")} (Spectating)`;
            }
            return "Spectating Table (Waiting for Hero Seat)...";
        }

        const h1 = heroCards[0].rank;
        const h2 = heroCards[1].rank;

        if (!boardCards || boardCards.length === 0) {
            if (h1 === h2) {
                return `Pocket ${h1}'s (Preflop)`;
            }
            return `Hole Cards (${h1}${heroCards[0].symbol} ${h2}${heroCards[1].symbol})`;
        }

        // Combine ranks for postflop
        const allRanks = [h1, h2, ...boardCards.map(c => c.rank)];
        const rankCounts = {};
        allRanks.forEach(r => {
            rankCounts[r] = (rankCounts[r] || 0) + 1;
        });

        const pairs = [];
        let threeOfAKind = null;
        let fourOfAKind = null;

        for (const [r, count] of Object.entries(rankCounts)) {
            if (count === 4) fourOfAKind = r;
            else if (count === 3) threeOfAKind = r;
            else if (count === 2) pairs.push(r);
        }

        if (fourOfAKind) return `Four of a Kind (${fourOfAKind}'s)`;
        if (threeOfAKind && pairs.length > 0) return `Full House (${threeOfAKind}'s full of ${pairs[0]}'s)`;
        if (threeOfAKind) return `Three of a Kind (${threeOfAKind}'s)`;
        if (pairs.length >= 2) return `Two Pair (${pairs[0]} & ${pairs[1]})`;
        if (pairs.length === 1) return `One Pair (${pairs[0]}'s)`;

        return `High Card (${h1 > h2 ? h1 : h2})`;
    }

    // =========================================================================
    // WebSocket Client
    // =========================================================================
    function connectWebSocket() {
        if (state.ws) {
            try { state.ws.close(); } catch (e) {}
            state.ws = null;
        }

        clearTimeout(state.reconnectTimer);

        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const host = window.location.host || "localhost:8080";
        const wsUrl = `${protocol}//${host}/ws/tables/${state.tableId}`;

        setConnectionStatus("searching", "○ Connecting...");

        try {
            state.ws = new WebSocket(wsUrl);

            state.ws.onopen = function () {
                setConnectionStatus("live", "● Live · Table Active");
                elements.hudWidget.classList.add("connected");
            };

            state.ws.onmessage = function (event) {
                try {
                    const msg = JSON.parse(event.data);
                    handleWSMessage(msg);
                } catch (err) {
                    console.error("Failed to parse WS message:", err);
                }
            };

            state.ws.onclose = function () {
                setConnectionStatus("offline", "● Reconnecting...");
                elements.hudWidget.classList.remove("connected", "hero-active");
                scheduleReconnect();
            };

            state.ws.onerror = function () {
                setConnectionStatus("offline", "● Offline");
                state.ws.close();
            };
        } catch (err) {
            setConnectionStatus("offline", "● Offline");
            scheduleReconnect();
        }
    }

    function scheduleReconnect() {
        clearTimeout(state.reconnectTimer);
        state.reconnectTimer = setTimeout(connectWebSocket, 2000);
    }

    function setConnectionStatus(type, text) {
        elements.hudStatusDot.className = "status-dot";
        if (type === "live") elements.hudStatusDot.classList.add("dot-live");
        else if (type === "searching") elements.hudStatusDot.classList.add("dot-searching");
        else elements.hudStatusDot.classList.add("dot-offline");

        elements.hudStatusText.textContent = text;
    }

    function handleWSMessage(msg) {
        if (!msg || !msg.type) return;

        switch (msg.type) {
            case "state_update":
                if (msg.payload) renderHandState(msg.payload);
                break;
            case "recommendation":
                if (msg.payload) renderAdvisorRecommendation(msg.payload);
                break;
            case "event":
                if (msg.payload && msg.payload.hand_state) {
                    renderHandState(msg.payload.hand_state);
                }
                break;
        }
    }

    // =========================================================================
    // UI Renderers
    // =========================================================================
    function renderHandState(handState) {
        state.currentHand = handState;
        if (!handState) return;

        // 1. Street Badge & Pot
        const street = (handState.street || "preflop").toLowerCase();
        elements.hudStreetBadge.className = `hud-street-badge street-${street}`;
        elements.hudStreetBadge.textContent = street.toUpperCase();
        elements.hudPotBadge.textContent = `Pot: ${formatMoney(handState.pot || 0)}`;

        // 2. Hero Cards
        const parsedHeroCards = [];
        if (handState.hero_cards && handState.hero_cards.length >= 2) {
            for (let i = 0; i < 2; i++) {
                const c = parseCardData(handState.hero_cards[i]);
                if (c && c.rank !== "0" && c.rank !== "?") {
                    parsedHeroCards.push(c);
                }
            }
        }

        const cardSlots = elements.hudHoleCards.querySelectorAll(".hud-card");
        if (parsedHeroCards.length === 2) {
            cardSlots.forEach((slot, idx) => {
                const c = parsedHeroCards[idx];
                slot.className = `hud-card ${c.suitClass}`;
                slot.querySelector(".card-rank").textContent = c.rank;
                slot.querySelector(".card-suit").textContent = c.symbol;
            });
        } else {
            cardSlots.forEach(slot => {
                slot.className = "hud-card card-empty";
                slot.querySelector(".card-rank").textContent = "?";
                slot.querySelector(".card-suit").textContent = "";
            });
        }

        // 3. Community Mini Board
        const boardCards = [];
        if (handState.community_cards && Array.isArray(handState.community_cards)) {
            handState.community_cards.forEach(raw => {
                const c = parseCardData(raw);
                if (c && c.rank !== "0" && c.rank !== "?") boardCards.push(c);
            });
        }

        const miniSlots = elements.hudCommunityMini.querySelectorAll(".mini-board-slot");
        miniSlots.forEach((slot, i) => {
            if (i < boardCards.length) {
                const c = boardCards[i];
                slot.className = `mini-board-slot ${c.suitClass}`;
                slot.textContent = `${c.rank}${c.symbol}`;
            } else {
                slot.className = "mini-board-slot empty";
                slot.textContent = "_";
            }
        });

        // 4. Hand Rank Label & Status
        elements.hudHandRank.textContent = determineMadeHandLabel(parsedHeroCards, boardCards);

        if (!state.currentAdvice) {
            elements.hudReasoningText.textContent = parsedHeroCards.length === 2
                ? "Hero cards recognized. Calculating optimal EV action..."
                : "Spectator Mode · Tracking table pot, bets, and opponent statistics...";
        }

        // 5. Sizing Matrix dynamic calculation
        updateSizingGrid(handState);

        // 6. Action History & Opponents
        renderActionHistory(handState.action_history || []);
        renderOpponentsList(handState.seats || [], handState.hero_id);
    }

    function renderAdvisorRecommendation(rec) {
        state.currentAdvice = rec;
        if (!rec) return;

        // 1. Primary Action Banner
        const action = (rec.primary_action || "CHECK").toUpperCase();
        elements.hudActionType.textContent = action;
        elements.hudActionAmount.textContent = rec.amount > 0 ? formatMoney(rec.amount) : "";
        
        elements.hudActionBanner.className = "hud-action-banner";
        if (action.includes("RAISE") || action.includes("BET")) {
            elements.hudActionBanner.classList.add("action-raise");
            elements.hudWidget.classList.add("hero-active");
            playTurnChime();
        } else if (action.includes("CALL")) {
            elements.hudActionBanner.classList.add("action-call");
            elements.hudWidget.classList.add("hero-active");
            playTurnChime();
        } else if (action.includes("ALL")) {
            elements.hudActionBanner.classList.add("action-allin");
            elements.hudWidget.classList.add("hero-active");
            playTurnChime();
        } else if (action.includes("FOLD")) {
            elements.hudActionBanner.classList.add("action-fold");
            elements.hudWidget.classList.remove("hero-active");
        } else {
            elements.hudWidget.classList.remove("hero-active");
        }

        const ev = rec.expected_value || 0;
        elements.hudEvBadge.textContent = `EV: ${ev >= 0 ? "+" : ""}${formatMoney(ev)}`;

        // 2. Dual Equity & Pot Odds Bars
        const eqPct = (rec.equity || 0) * 100;
        const poPct = (rec.pot_odds || 0) * 100;

        elements.hudEquityVal.textContent = `${eqPct.toFixed(1)}%`;
        elements.hudEquityBar.style.width = `${Math.min(100, Math.max(0, eqPct))}%`;

        elements.hudPotOddsVal.textContent = `${poPct.toFixed(1)}%`;
        elements.hudPotOddsBar.style.width = `${Math.min(100, Math.max(0, poPct))}%`;

        // 3. Verdict Pill
        if (rec.pot_odds > 0) {
            if (rec.equity >= rec.pot_odds) {
                const diff = (eqPct - poPct).toFixed(1);
                elements.hudVerdictPill.className = "hud-verdict-pill positive";
                elements.hudVerdictIcon.textContent = "▲";
                elements.hudVerdictText.textContent = `+EV Call (Equity > Pot Odds +${diff}%)`;
            } else {
                const diff = (poPct - eqPct).toFixed(1);
                elements.hudVerdictPill.className = "hud-verdict-pill negative";
                elements.hudVerdictIcon.textContent = "▼";
                elements.hudVerdictText.textContent = `-EV Call (Deficit -${diff}%)`;
            }
        } else {
            elements.hudVerdictPill.className = "hud-verdict-pill";
            elements.hudVerdictIcon.textContent = "⚖";
            elements.hudVerdictText.textContent = `Hero Equity Edge: ${eqPct.toFixed(1)}%`;
        }

        // 4. Tactical Reasoning
        if (rec.reasoning) {
            elements.hudReasoningText.textContent = rec.reasoning;
        }
    }

    function updateSizingGrid(handState) {
        const pot = handState.pot || 0;
        const curBet = handState.current_bet || 0;
        const minRaise = handState.min_raise || Math.max(2, curBet * 2);

        elements.sizeAmtMin.textContent = formatMoney(minRaise);
        elements.sizeAmt25x.textContent = formatMoney(Math.max(minRaise, curBet * 2.5 || minRaise));
        elements.sizeAmt33.textContent = formatMoney(Math.max(minRaise, curBet + pot * 0.33));
        elements.sizeAmt66.textContent = formatMoney(Math.max(minRaise, curBet + pot * 0.66));
        elements.sizeAmtPot.textContent = formatMoney(Math.max(minRaise, curBet + pot));

        // Find Hero Stack for All-In
        let heroStack = 200;
        if (handState.seats && Array.isArray(handState.seats)) {
            const heroSeat = handState.seats.find(s => s.player_id === handState.hero_id || s.seat_number === 0);
            if (heroSeat && heroSeat.stack > 0) heroStack = heroSeat.stack;
        }
        elements.sizeAmtAllIn.textContent = formatMoney(heroStack);
    }

    function renderActionHistory(actions) {
        elements.actionCount.textContent = actions.length;
        if (!actions || actions.length === 0) {
            elements.hudActionsList.innerHTML = '<div class="empty-actions">No hand actions recorded.</div>';
            return;
        }

        const recent = actions.slice(-8).reverse();
        elements.hudActionsList.innerHTML = recent.map(a => `
            <div class="act-entry">
                <span class="act-player">${escapeHtml(a.player_name || a.player_id || "Player")}</span>
                <span class="act-desc">${escapeHtml(a.action || "action")}${a.amount > 0 ? " " + formatMoney(a.amount) : ""}</span>
            </div>
        `).join("");
    }

    function renderOpponentsList(seats, heroId) {
        const opponents = seats.filter(s => s.player_id && s.player_id !== heroId && s.is_active && !s.is_folded);
        if (opponents.length === 0) {
            elements.hudOpponentsList.innerHTML = '<div class="empty-opponents">No active opponents in hand.</div>';
            return;
        }

        elements.hudOpponentsList.innerHTML = opponents.map(s => {
            const cached = state.playerProfiles.get(s.player_id);
            const stats = cached ? cached.stats : null;
            const profile = cached ? cached.profile : null;

            const vpip = stats ? stats.vpip.toFixed(0) : "--";
            const pfr = stats ? stats.pfr.toFixed(0) : "--";
            const af = stats ? stats.af.toFixed(1) : "--";
            const arch = profile ? profile.archetype || "UNKNOWN" : "UNKNOWN";
            const archClass = `arch-${arch.toLowerCase().replace(/[^a-z]/g, "")}` || "arch-tag";
            const exploit = profile ? profile.exploits : "Gathering table statistics...";

            // Trigger lazy fetch if not cached
            if (!cached && s.player_id) {
                fetchPlayerProfile(s.player_id);
            }

            return `
                <div class="opp-card">
                    <div class="opp-card-header">
                        <span class="opp-name">${escapeHtml(s.player_name || s.player_id)} (Seat ${s.seat_number})</span>
                        <span class="opp-badge-arch ${archClass}">${escapeHtml(arch)}</span>
                    </div>
                    <div class="opp-stats-row">
                        <span>VPIP: <strong>${vpip}%</strong></span>
                        <span>PFR: <strong>${pfr}%</strong></span>
                        <span>AF: <strong>${af}</strong></span>
                        <span>Stack: <strong>${formatMoney(s.stack)}</strong></span>
                    </div>
                    ${exploit ? `<div class="opp-exploit-text">💡 ${escapeHtml(exploit)}</div>` : ""}
                </div>
            `;
        }).join("");
    }

    async function fetchPlayerProfile(playerId) {
        try {
            const res = await fetch(`/api/v1/players/${encodeURIComponent(playerId)}/profile`);
            if (res.ok) {
                const data = await res.json();
                state.playerProfiles.set(playerId, data);
                if (state.currentHand && state.currentHand.seats) {
                    renderOpponentsList(state.currentHand.seats, state.currentHand.hero_id);
                }
            }
        } catch (e) {
            // Player stats not available yet
        }
    }

    function escapeHtml(str) {
        if (!str) return "";
        return String(str)
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;");
    }

    // =========================================================================
    // Simulation Helper Actions
    // =========================================================================
    async function sendSimEvent(eventType, handState, desc) {
        try {
            const res = await fetch(`/api/v1/tables/${encodeURIComponent(state.tableId)}/events`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    type: eventType,
                    table_id: state.tableId,
                    hand_state: handState,
                    description: desc
                })
            });
            if (res.ok) {
                const data = await res.json();
                if (data.recommendation) {
                    renderAdvisorRecommendation(data.recommendation);
                }
            }
        } catch (err) {
            console.error("Simulation request failed:", err);
        }
    }

    function getBaseSimState() {
        return {
            hand_id: `sim-hand-${Date.now()}`,
            table_id: state.tableId,
            street: "preflop",
            pot: 15.0,
            current_bet: 2.0,
            min_raise: 4.0,
            hero_id: "hero-1",
            hero_cards: [
                { rank: 14, suit: "s" }, // A♠
                { rank: 13, suit: "d" }  // K♦
            ],
            community_cards: [],
            seats: [
                { seat_number: 0, player_id: "hero-1", player_name: "Hero", stack: 195.0, current_bet: 2.0, is_active: true, is_hero: true },
                { seat_number: 1, player_id: "villain-1", player_name: "FishCaller", stack: 180.0, current_bet: 2.0, is_active: true },
                { seat_number: 2, player_id: "villain-2", player_name: "AggroShark", stack: 240.0, current_bet: 0.0, is_active: true }
            ],
            action_history: [
                { player_id: "villain-1", street: "preflop", action: "call", amount: 2.0 },
                { player_id: "hero-1", street: "preflop", action: "raise", amount: 6.0 }
            ]
        };
    }

    // =========================================================================
    // Event Listeners & UI Binding
    // =========================================================================
    function setupEventListeners() {
        // Toggle Opponents & Actions Drawer
        elements.btnToggleStats.addEventListener("click", () => {
            elements.hudOpponentsDrawer.classList.toggle("collapsed");
        });

        // Toggle Audio Chime
        elements.btnToggleSound.addEventListener("click", () => {
            state.soundEnabled = !state.soundEnabled;
            elements.btnToggleSound.textContent = state.soundEnabled ? "🔔" : "🔕";
        });

        // Toggle Settings Modal
        elements.btnHudSettings.addEventListener("click", () => {
            elements.hudSettingsModal.classList.toggle("hidden");
        });
        elements.btnCloseSettings.addEventListener("click", () => {
            elements.hudSettingsModal.classList.add("hidden");
        });

        // Tabs in Drawer
        elements.tabOpponents.addEventListener("click", () => {
            elements.tabOpponents.classList.add("active");
            elements.tabActions.classList.remove("active");
            elements.paneOpponents.classList.add("active");
            elements.paneActions.classList.remove("active");
        });
        elements.tabActions.addEventListener("click", () => {
            elements.tabActions.classList.add("active");
            elements.tabOpponents.classList.remove("active");
            elements.paneActions.classList.add("active");
            elements.paneOpponents.classList.remove("active");
        });

        // Opacity Slider
        elements.sliderOpacity.addEventListener("input", (e) => {
            elements.hudWidget.style.opacity = e.target.value / 100;
        });

        // Reconnect Table ID
        elements.btnReconnect.addEventListener("click", () => {
            const newId = elements.inputTableId.value.trim();
            if (newId) {
                state.tableId = newId;
                connectWebSocket();
                elements.hudSettingsModal.classList.add("hidden");
            }
        });

        // Sizing Chip Click (Copy to clipboard or notification)
        elements.sizeAmtMin.parentElement.parentElement.addEventListener("click", (e) => {
            const btn = e.target.closest(".sizing-chip-btn");
            if (btn) {
                const amtText = btn.querySelector(".s-amt").textContent;
                navigator.clipboard && navigator.clipboard.writeText(amtText.replace("$", ""));
                btn.style.transform = "scale(0.95)";
                setTimeout(() => { btn.style.transform = ""; }, 150);
            }
        });

        // Simulation Test Triggers
        elements.simBtnNewHand.addEventListener("click", () => {
            const st = getBaseSimState();
            sendSimEvent("hand_start", st, "Simulated New Hand Preflop");
        });

        elements.simBtnFlop.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "flop";
            st.pot = 35.0;
            st.community_cards = [
                { rank: 14, suit: "h" }, // A♥
                { rank: 13, suit: "c" }, // K♣
                { rank: 2, suit: "d" }   // 2♦
            ];
            sendSimEvent("card_dealt", st, "Flop Dealt: A♥ K♣ 2♦");
        });

        elements.simBtnTurn.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "turn";
            st.pot = 65.0;
            st.community_cards = [
                { rank: 14, suit: "h" },
                { rank: 13, suit: "c" },
                { rank: 2, suit: "d" },
                { rank: 11, suit: "s" }  // J♠
            ];
            sendSimEvent("card_dealt", st, "Turn Dealt: J♠");
        });

        elements.simBtnRiver.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "river";
            st.pot = 120.0;
            st.community_cards = [
                { rank: 14, suit: "h" },
                { rank: 13, suit: "c" },
                { rank: 2, suit: "d" },
                { rank: 11, suit: "s" },
                { rank: 8, suit: "h" }   // 8♥
            ];
            sendSimEvent("card_dealt", st, "River Dealt: 8♥");
        });

        elements.simBtnBet.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "flop";
            st.pot = 55.0;
            st.current_bet = 20.0;
            st.community_cards = [
                { rank: 14, suit: "h" },
                { rank: 13, suit: "c" },
                { rank: 2, suit: "d" }
            ];
            st.action_history.push({ player_id: "villain-1", street: "flop", action: "bet", amount: 20.0 });
            sendSimEvent("bet", st, "Villain bet $20.00");
        });

        elements.simBtnHeroTurn.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "flop";
            st.pot = 55.0;
            st.current_bet = 20.0;
            st.community_cards = [
                { rank: 14, suit: "h" },
                { rank: 13, suit: "c" },
                { rank: 2, suit: "d" }
            ];
            sendSimEvent("hero_turn", st, "Hero Action Decision facing $20 bet");
        });

        elements.simBtnEnd.addEventListener("click", () => {
            const st = getBaseSimState();
            st.street = "showdown";
            st.pot = 120.0;
            sendSimEvent("hand_end", st, "Hand Completed");
        });
    }

    // =========================================================================
    // Initialization
    // =========================================================================
    function init() {
        setupEventListeners();
        connectWebSocket();
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
