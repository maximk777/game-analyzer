/**
 * POKER RTA HUD & REAL-TIME ANALYZER
 * Client Application Logic
 */

(function () {
    "use strict";

    // Application State
    const state = {
        tableId: "table-1",
        ws: null,
        isPaused: false,
        reconnectTimer: null,
        eventCount: 0,
        playerProfiles: new Map(), // playerID -> profile
        currentHand: null,
        currentAdvice: null,
        selectedPlayerId: null,
    };

    // DOM Elements
    const elements = {
        tableIdInput: document.getElementById("tableIdInput"),
        btnConnect: document.getElementById("btnConnect"),
        btnPauseResume: document.getElementById("btnPauseResume"),
        connectionPill: document.getElementById("connectionPill"),
        connectionText: document.getElementById("connectionText"),
        latencyMetric: document.getElementById("latencyMetric"),
        handIdDisplay: document.getElementById("handIdDisplay"),
        streetBadge: document.getElementById("streetBadge"),
        currentBetDisplay: document.getElementById("currentBetDisplay"),
        minRaiseDisplay: document.getElementById("minRaiseDisplay"),
        potDisplay: document.getElementById("potDisplay"),
        communityCardsContainer: document.getElementById("communityCardsContainer"),
        seatsContainer: document.getElementById("seatsContainer"),
        eventsLogList: document.getElementById("eventsLogList"),
        eventCountBadge: document.getElementById("eventCountBadge"),
        heroCardsContainer: document.getElementById("heroCardsContainer"),
        equityValueDisplay: document.getElementById("equityValueDisplay"),
        equityBarFill: document.getElementById("equityBarFill"),
        potOddsValueDisplay: document.getElementById("potOddsValueDisplay"),
        potOddsBarFill: document.getElementById("potOddsBarFill"),
        evVerdictPill: document.getElementById("evVerdictPill"),
        evVerdictText: document.getElementById("evVerdictText"),
        primaryActionBox: document.getElementById("primaryActionBox"),
        primaryActionText: document.getElementById("primaryActionText"),
        primaryActionAmount: document.getElementById("primaryActionAmount"),
        primaryActionEV: document.getElementById("primaryActionEV"),
        sizingGrid: document.getElementById("sizingGrid"),
        reasoningText: document.getElementById("reasoningText"),
        inspectPlayerName: document.getElementById("inspectPlayerName"),
        opponentInspectBody: document.getElementById("opponentInspectBody"),
    };

    // Card Rank & Suit Mappings
    const SUIT_SYMBOLS = {
        0: "♠", 1: "♥", 2: "♦", 3: "♣",
        "s": "♠", "h": "♥", "d": "♦", "c": "♣"
    };

    const SUIT_CLASSES = {
        0: "suit-s", 1: "suit-h", 2: "suit-d", 3: "suit-c",
        "s": "suit-s", "h": "suit-h", "d": "suit-d", "c": "suit-c"
    };

    const RANK_LABELS = {
        2: "2", 3: "3", 4: "4", 5: "5", 6: "6", 7: "7", 8: "8", 9: "9",
        10: "T", 11: "J", 12: "Q", 13: "K", 14: "A"
    };

    // Format Card Object or String to { rank, suit, suitClass, symbol }
    function parseCardData(card) {
        if (!card) return null;
        if (typeof card === "string") {
            if (card.length < 2) return null;
            const r = card.slice(0, -1).toUpperCase();
            const s = card.slice(-1).toLowerCase();
            return {
                rank: r,
                suit: s,
                suitClass: SUIT_CLASSES[s] || "suit-s",
                symbol: SUIT_SYMBOLS[s] || "♠"
            };
        }
        if (typeof card === "object") {
            const r = RANK_LABELS[card.rank] || (card.rank ? String(card.rank) : "?");
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

    // Format Currency
    function formatMoney(amount) {
        if (amount === undefined || amount === null || isNaN(amount)) return "$0.00";
        return "$" + Number(amount).toFixed(2);
    }

    // Format Percentage
    function formatPct(val) {
        if (val === undefined || val === null || isNaN(val)) return "0.0%";
        const num = val > 1.0 ? val : val * 100;
        return num.toFixed(1) + "%";
    }

    // Append to Event Log
    function addLogEntry(text, type = "info") {
        state.eventCount++;
        elements.eventCountBadge.textContent = `${state.eventCount} events`;

        const now = new Date();
        const timeStr = now.toTimeString().split(" ")[0];

        const entry = document.createElement("div");
        entry.className = `event-log-entry ${type}`;
        entry.innerHTML = `<span class="timestamp">[${timeStr}]</span> <span class="event-text">${escapeHtml(text)}</span>`;

        elements.eventsLogList.appendChild(entry);
        elements.eventsLogList.scrollTop = elements.eventsLogList.scrollHeight;

        // Keep maximum 100 entries
        while (elements.eventsLogList.children.length > 100) {
            elements.eventsLogList.removeChild(elements.eventsLogList.firstChild);
        }
    }

    function escapeHtml(str) {
        return String(str).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    }

    // Connect WebSocket
    function connectWS() {
        if (state.ws) {
            state.ws.close();
            state.ws = null;
        }

        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const host = window.location.host || "localhost:8080";
        const wsUrl = `${protocol}//${host}/ws/tables/${encodeURIComponent(state.tableId)}`;

        updateConnectionStatus("connecting", "Connecting...");

        try {
            const ws = new WebSocket(wsUrl);

            ws.onopen = function () {
                updateConnectionStatus("connected", `Connected (${state.tableId})`);
                addLogEntry(`Connected to table ${state.tableId} WebSocket channel.`, "info");
            };

            ws.onmessage = function (event) {
                if (state.isPaused) return;

                try {
                    const msg = JSON.parse(event.data);
                    handleWSMessage(msg);
                } catch (err) {
                    console.error("Failed to parse WS message:", err, event.data);
                }
            };

            ws.onclose = function () {
                updateConnectionStatus("disconnected", "Disconnected");
                addLogEntry(`WebSocket connection closed. Retrying in 3s...`, "info");
                scheduleReconnect();
            };

            ws.onerror = function (err) {
                console.error("WS error:", err);
                updateConnectionStatus("disconnected", "Connection Error");
            };

            state.ws = ws;
        } catch (e) {
            console.error("WS connection exception:", e);
            updateConnectionStatus("disconnected", "Failed to connect");
            scheduleReconnect();
        }
    }

    function scheduleReconnect() {
        if (state.reconnectTimer) clearTimeout(state.reconnectTimer);
        state.reconnectTimer = setTimeout(() => {
            connectWS();
        }, 3000);
    }

    function updateConnectionStatus(status, text) {
        elements.connectionPill.className = `status-pill status-${status}`;
        elements.connectionText.textContent = text;
    }

    // Handle WebSocket Incoming Messages
    function handleWSMessage(msg) {
        if (!msg) return;

        // Latency metric
        if (msg.timestamp) {
            const lat = Math.max(0, Date.now() - msg.timestamp);
            elements.latencyMetric.textContent = `${lat} ms`;
        }

        switch (msg.type) {
            case "state_update":
                renderHandState(msg.payload);
                break;
            case "recommendation":
                renderRecommendation(msg.payload);
                break;
            case "event":
                renderVisionEvent(msg.payload);
                break;
            case "ping":
                if (state.ws && state.ws.readyState === WebSocket.OPEN) {
                    state.ws.send(JSON.stringify({ type: "pong" }));
                }
                break;
            default:
                break;
        }
    }

    // Render Hand State
    function renderHandState(hand) {
        if (!hand) return;
        state.currentHand = hand;

        elements.handIdDisplay.textContent = hand.hand_id || "--";
        
        // Street Badge
        const street = (hand.street || "preflop").toLowerCase();
        elements.streetBadge.textContent = street.toUpperCase();
        elements.streetBadge.className = `street-badge street-${street}`;

        // Header Metrics
        elements.currentBetDisplay.textContent = formatMoney(hand.current_bet);
        elements.minRaiseDisplay.textContent = formatMoney(hand.min_raise);
        elements.potDisplay.textContent = formatMoney(hand.pot);

        // Community Cards
        renderCommunityCards(hand.community_cards || []);

        // Hero Cards
        if (hand.hero_cards && hand.hero_cards.length >= 2) {
            renderHeroCards(hand.hero_cards);
        }

        // Seats Layout (6-Max)
        renderSeats(hand.seats || [], hand.hero_id, hand.current_bet);
    }

    // Render Community Cards
    function renderCommunityCards(cards) {
        const slots = elements.communityCardsContainer.querySelectorAll(".card-slot");
        slots.forEach((slot, idx) => {
            const card = cards[idx];
            if (card) {
                const parsed = parseCardData(card);
                if (parsed) {
                    slot.className = "card-slot";
                    slot.innerHTML = `
                        <div class="card ${parsed.suitClass}">
                            <div class="card-rank">${parsed.rank}</div>
                            <div class="card-suit">${parsed.symbol}</div>
                        </div>
                    `;
                    return;
                }
            }
            // Empty slot placeholder
            const labels = ["Flop", "Flop", "Flop", "Turn", "River"];
            slot.className = "card-slot empty";
            slot.innerHTML = `<span class="slot-placeholder">${labels[idx] || "Card"}</span>`;
        });
    }

    // Render Hero Cards
    function renderHeroCards(cards) {
        elements.heroCardsContainer.innerHTML = "";
        cards.forEach((c) => {
            const parsed = parseCardData(c);
            if (parsed && parsed.rank !== "?") {
                const cardEl = document.createElement("div");
                cardEl.className = `mini-card ${parsed.suitClass}`;
                cardEl.innerHTML = `<span>${parsed.rank}${parsed.symbol}</span>`;
                elements.heroCardsContainer.appendChild(cardEl);
            } else {
                const cardEl = document.createElement("div");
                cardEl.className = "mini-card empty";
                cardEl.textContent = "?";
                elements.heroCardsContainer.appendChild(cardEl);
            }
        });
    }

    // Render 6-Max Player Seats
    function renderSeats(seats, heroId, currentBet) {
        elements.seatsContainer.innerHTML = "";

        // Default 6 positions if empty
        const effectiveSeats = seats.length > 0 ? seats : [
            { seat_number: 0, player_id: "hero", player_name: "Hero", stack: 200, is_active: true, position: "BTN" },
            { seat_number: 1, player_id: "villain-1", player_name: "mamayazareyzil", stack: 195, is_active: true, position: "SB" },
            { seat_number: 2, player_id: "villain-2", player_name: "AbdulTaxi", stack: 210, is_active: true, position: "BB" },
            { seat_number: 3, player_id: "villain-3", player_name: "mko6969", stack: 180, is_active: true, position: "UTG" },
            { seat_number: 4, player_id: "villain-4", player_name: "HundPatron", stack: 250, is_active: true, position: "MP" },
            { seat_number: 5, player_id: "villain-5", player_name: "internazional", stack: 190, is_active: true, position: "CO" },
        ];

        effectiveSeats.forEach((seat, idx) => {
            const seatNumber = seat.seat_number !== undefined ? seat.seat_number : idx;
            const isHero = seat.player_id === heroId || seatNumber === 0 || seat.is_hero;
            const isFolded = seat.is_folded;
            const posClass = `seat-pos-${seatNumber % 6}`;

            const seatEl = document.createElement("div");
            seatEl.className = `player-seat ${posClass} ${isHero ? "is-hero" : ""} ${isFolded ? "is-folded" : ""}`;
            seatEl.setAttribute("data-player-id", seat.player_id || `player-${seatNumber}`);

            // Fetch profile for HUD overlay stats if available
            const prof = state.playerProfiles.get(seat.player_id) || getSimulatedProfile(seat.player_id, seat.player_name);

            const vpip = prof && prof.stats ? prof.stats.vpip : (prof && prof.tendencies ? prof.tendencies.vpip : 25);
            const pfr = prof && prof.stats ? prof.stats.pfr : (prof && prof.tendencies ? prof.tendencies.pfr : 18);
            const af = prof && prof.stats ? prof.stats.af : (prof && prof.tendencies ? prof.tendencies.af : 2.1);
            const archetype = prof && prof.profile ? prof.profile.archetype : (prof && prof.archetype ? prof.archetype : "TAG");
            const archClass = `arch-${archetype.replace(/[^a-zA-Z]/g, "")}`;

            // HUD stats pill above player
            let hudPillHtml = "";
            if (!isHero) {
                hudPillHtml = `
                    <div class="opponent-hud-pill">
                        <span class="archetype-pill ${archClass}">${escapeHtml(archetype)}</span>
                        <div class="hud-stat-item"><span class="hud-stat-label">V:</span><span class="hud-stat-val">${Math.round(vpip)}%</span></div>
                        <div class="hud-stat-item"><span class="hud-stat-label">P:</span><span class="hud-stat-val">${Math.round(pfr)}%</span></div>
                        <div class="hud-stat-item"><span class="hud-stat-label">AF:</span><span class="hud-stat-val">${af}</span></div>
                    </div>
                `;
            }

            seatEl.innerHTML = `
                ${hudPillHtml}
                <div class="seat-card-body">
                    <div class="seat-avatar">${isHero ? "★" : (seat.player_name ? seat.player_name[0].toUpperCase() : "P")}</div>
                    <div class="seat-info">
                        <div class="seat-name">${escapeHtml(seat.player_name || seat.player_id || "Player")}</div>
                        <div class="seat-stack">${formatMoney(seat.stack)}</div>
                    </div>
                    ${seat.position ? `<div class="position-badge">${seat.position}</div>` : ""}
                </div>
                ${seat.current_bet > 0 ? `<div class="seat-bet-chip">${formatMoney(seat.current_bet)}</div>` : ""}
            `;

            seatEl.addEventListener("click", () => {
                inspectPlayer(seat.player_id, seat.player_name, prof);
            });

            elements.seatsContainer.appendChild(seatEl);
        });
    }

    // Fallback heuristic profile for CoinPoker players before first DB response
    function getSimulatedProfile(playerId, playerName) {
        const name = playerName || playerId || "";
        if (name.includes("mama")) {
            return {
                player_id: playerId,
                player_name: name,
                archetype: "LAG",
                stats: { vpip: 38.2, pfr: 29.1, three_bet: 9.5, af: 2.8, hands_count: 54 },
                profile: {
                    archetype: "LAG",
                    bluff_frequency: 0.35,
                    fold_to_3bet: 0.40,
                    fold_to_cbet: 0.40,
                    exploits: "Induce bluffs and call down lighter on non-scare rivers.",
                    notes: "High preflop aggression and barrel frequency."
                }
            };
        } else if (name.includes("Abdul") || name.includes("Taxi")) {
            return {
                player_id: playerId,
                player_name: name,
                archetype: "Fish/Whale",
                stats: { vpip: 62.0, pfr: 10.5, three_bet: 2.0, af: 0.9, hands_count: 32 },
                profile: {
                    archetype: "Fish/Whale",
                    bluff_frequency: 0.10,
                    fold_to_3bet: 0.20,
                    fold_to_cbet: 0.30,
                    exploits: "Value bet relentlessly with top pair+; never bluff.",
                    notes: "Calling station who chases all flush and straight draws."
                }
            };
        } else if (name.includes("Hund")) {
            return {
                player_id: playerId,
                player_name: name,
                archetype: "Nit",
                stats: { vpip: 14.5, pfr: 12.0, three_bet: 4.0, af: 1.8, hands_count: 88 },
                profile: {
                    archetype: "Nit",
                    bluff_frequency: 0.05,
                    fold_to_3bet: 0.80,
                    fold_to_cbet: 0.70,
                    exploits: "Steal blinds continuously; fold whenever they show aggression.",
                    notes: "Only enters pots with premiums."
                }
            };
        }
        return {
            player_id: playerId,
            player_name: name,
            archetype: "TAG",
            stats: { vpip: 22.5, pfr: 18.0, three_bet: 7.2, af: 2.4, hands_count: 45 },
            profile: {
                archetype: "TAG",
                bluff_frequency: 0.20,
                fold_to_3bet: 0.55,
                fold_to_cbet: 0.45,
                exploits: "Attack checked turns/rivers; respect triple barrel raises.",
                notes: "Solid regular with balanced ranges."
            }
        };
    }

    // Inspect Player in Sidebar
    function inspectPlayer(playerId, playerName, profile) {
        state.selectedPlayerId = playerId;
        elements.inspectPlayerName.textContent = playerName || playerId;

        if (!profile) {
            elements.opponentInspectBody.innerHTML = `<p class="placeholder-text">Loading profile for ${escapeHtml(playerName)}...</p>`;
            fetchPlayerProfile(playerId);
            return;
        }

        const stats = profile.stats || {};
        const prof = profile.profile || {};

        elements.opponentInspectBody.innerHTML = `
            <div class="stat-grid-inspect">
                <div class="stat-inspect-item"><div class="num">${stats.vpip ? Math.round(stats.vpip) : "--"}%</div><div class="lbl">VPIP</div></div>
                <div class="stat-inspect-item"><div class="num">${stats.pfr ? Math.round(stats.pfr) : "--"}%</div><div class="lbl">PFR</div></div>
                <div class="stat-inspect-item"><div class="num">${stats.three_bet ? Math.round(stats.three_bet) : "--"}%</div><div class="lbl">3-Bet</div></div>
                <div class="stat-inspect-item"><div class="num">${stats.af !== undefined ? stats.af : "--"}</div><div class="lbl">AF</div></div>
            </div>
            <div style="margin-bottom: 8px;">
                <span class="archetype-pill arch-${(prof.archetype || "TAG").replace(/[^a-zA-Z]/g, "")}">${prof.archetype || "Unknown Archetype"}</span>
                <span style="font-size: 0.75rem; color: var(--text-muted); margin-left: 8px;">Bluff Freq: ${formatPct(prof.bluff_frequency || 0.2)}</span>
            </div>
            <div class="exploit-notes-box">
                <strong>Exploitative Strategy:</strong><br>
                ${escapeHtml(prof.exploits || "Standard balanced play.")}
            </div>
            ${prof.notes ? `<div style="font-size: 0.75rem; color: var(--text-secondary); margin-top: 6px;"><em>Notes:</em> ${escapeHtml(prof.notes)}</div>` : ""}
        `;
    }

    // Fetch Profile via REST API
    function fetchPlayerProfile(playerId) {
        fetch(`/api/v1/players/${encodeURIComponent(playerId)}/profile`)
            .then(res => res.ok ? res.json() : null)
            .then(data => {
                if (data) {
                    state.playerProfiles.set(playerId, data);
                    if (state.selectedPlayerId === playerId) {
                        inspectPlayer(playerId, data.player_id, data);
                    }
                }
            })
            .catch(err => console.warn("Failed to fetch player profile:", err));
    }

    // Render Real-Time RTA Recommendation
    function renderRecommendation(advice) {
        if (!advice) return;
        state.currentAdvice = advice;

        // Equity & Pot Odds
        const eq = advice.equity || 0.0;
        const potOdds = advice.pot_odds || 0.0;

        elements.equityValueDisplay.textContent = formatPct(eq);
        elements.equityBarFill.style.width = `${Math.min(100, Math.max(0, eq * 100))}%`;

        elements.potOddsValueDisplay.textContent = formatPct(potOdds);
        elements.potOddsBarFill.style.width = `${Math.min(100, Math.max(0, potOdds * 100))}%`;

        // EV Verdict Pill
        if (eq > potOdds) {
            elements.evVerdictPill.className = "ev-comparison-pill profitable";
            elements.evVerdictPill.innerHTML = `<span class="verdict-icon">✔</span> <span>Equity (${formatPct(eq)}) &gt; Pot Odds (${formatPct(potOdds)}) &rarr; <strong>+EV Profitable Decision</strong></span>`;
        } else {
            elements.evVerdictPill.className = "ev-comparison-pill";
            elements.evVerdictPill.innerHTML = `<span class="verdict-icon">⚠</span> <span>Equity (${formatPct(eq)}) &le; Pot Odds (${formatPct(potOdds)}) &rarr; <strong>Fold / Negative EV</strong></span>`;
        }

        // Primary Action
        const primaryAction = (advice.primary_action || "check").toUpperCase();
        elements.primaryActionText.textContent = primaryAction;
        elements.primaryActionAmount.textContent = advice.recommended_amount > 0 ? formatMoney(advice.recommended_amount) : "";

        // Find EV of primary action
        let primaryEV = 0.0;
        if (advice.actions) {
            const found = advice.actions.find(a => a.is_primary || a.action === advice.primary_action);
            if (found) primaryEV = found.ev;
        }
        elements.primaryActionEV.textContent = `EV: ${primaryEV >= 0 ? "+" : ""}${formatMoney(primaryEV)}`;

        // Sizing Matrix
        renderSizingMatrix(advice.actions || []);

        // Tactical Reasoning
        elements.reasoningText.textContent = advice.reasoning || "Balanced play according to Monte Carlo simulation.";

        addLogEntry(`Advisor: Recommended ${primaryAction} (${formatMoney(advice.recommended_amount)}) | Equity: ${formatPct(eq)}`, "advice");
    }

    // Render Sizing Matrix Grid
    function renderSizingMatrix(actions) {
        elements.sizingGrid.innerHTML = "";

        if (!actions || actions.length === 0) {
            elements.sizingGrid.innerHTML = `<div class="sizing-btn disabled"><span class="btn-label">No actions</span></div>`;
            return;
        }

        actions.forEach(act => {
            const btn = document.createElement("div");
            btn.className = `sizing-btn ${act.is_primary ? "primary" : ""}`;

            const label = act.sizing_label || act.action.toUpperCase();
            const amtStr = act.amount > 0 ? ` (${formatMoney(act.amount)})` : "";
            const evStr = `EV: ${act.ev >= 0 ? "+" : ""}${Number(act.ev).toFixed(2)}`;

            btn.innerHTML = `
                <span class="btn-label">${escapeHtml(label)}${amtStr}</span>
                <span class="btn-ev">${evStr}</span>
            `;

            elements.sizingGrid.appendChild(btn);
        });
    }

    // Render Vision Event
    function renderVisionEvent(event) {
        if (!event) return;

        let logType = "action";
        if (event.type === "CardDealt") logType = "card";
        if (event.type === "HeroTurn") logType = "advice";

        addLogEntry(`[${event.type}] ${event.description || "Event ingested"}`, logType);

        if (event.hand_state) {
            renderHandState(event.hand_state);
        }
    }

    // Send Test Event to Server
    function sendTestEvent(eventPayload) {
        const tableId = state.tableId;
        fetch(`/api/v1/tables/${encodeURIComponent(tableId)}/events`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(eventPayload)
        })
        .then(res => res.json())
        .then(data => {
            if (data.recommendation) {
                renderRecommendation(data.recommendation);
            }
        })
        .catch(err => {
            console.error("Failed to send test event:", err);
            addLogEntry(`Error posting test event: ${err.message}`, "info");
        });
    }

    // Setup Test Simulation Triggers
    function setupTestTriggers() {
        document.getElementById("btnTestStart").addEventListener("click", () => {
            const handState = {
                hand_id: `hand-${Date.now()}`,
                table_id: state.tableId,
                street: "preflop",
                pot: 3.0,
                current_bet: 2.0,
                min_raise: 4.0,
                hero_id: "player-0",
                hero_cards: [{ rank: 14, suit: 0 }, { rank: 13, suit: 1 }], // As Kh
                community_cards: [],
                seats: [
                    { seat_number: 0, player_id: "player-0", player_name: "Hero", stack: 200, current_bet: 0, is_active: true, position: "BTN" },
                    { seat_number: 1, player_id: "player-1", player_name: "mamayazareyzil", stack: 198, current_bet: 1, is_active: true, position: "SB" },
                    { seat_number: 2, player_id: "player-2", player_name: "AbdulTaxi", stack: 198, current_bet: 2, is_active: true, position: "BB" },
                    { seat_number: 3, player_id: "player-3", player_name: "mko6969", stack: 200, current_bet: 0, is_active: true, position: "UTG" },
                    { seat_number: 4, player_id: "player-4", player_name: "HundPatron", stack: 200, current_bet: 0, is_active: true, position: "MP" },
                    { seat_number: 5, player_id: "player-5", player_name: "internazional", stack: 200, current_bet: 0, is_active: true, position: "CO" },
                ],
                action_history: []
            };

            sendTestEvent({
                type: "HandStart",
                table_id: state.tableId,
                hand_state: handState,
                description: "Hand started. Hero dealt A♠ K♥ on BTN."
            });
        });

        document.getElementById("btnTestFlop").addEventListener("click", () => {
            if (!state.currentHand) return;
            const h = Object.assign({}, state.currentHand);
            h.street = "flop";
            h.pot = 12.0;
            h.community_cards = [
                { rank: 14, suit: 0 }, // As
                { rank: 13, suit: 2 }, // Kd
                { rank: 2, suit: 3 }   // 2c
            ];

            sendTestEvent({
                type: "CardDealt",
                table_id: state.tableId,
                hand_state: h,
                description: "Flop dealt: A♠ K♦ 2♣ (Pot: $12.00)"
            });
        });

        document.getElementById("btnTestTurn").addEventListener("click", () => {
            if (!state.currentHand) return;
            const h = Object.assign({}, state.currentHand);
            h.street = "turn";
            h.pot = 28.0;
            h.community_cards = [
                { rank: 14, suit: 0 }, // As
                { rank: 13, suit: 2 }, // Kd
                { rank: 2, suit: 3 },  // 2c
                { rank: 11, suit: 1 }  // Jh
            ];

            sendTestEvent({
                type: "CardDealt",
                table_id: state.tableId,
                hand_state: h,
                description: "Turn dealt: J♥ (Pot: $28.00)"
            });
        });

        document.getElementById("btnTestRiver").addEventListener("click", () => {
            if (!state.currentHand) return;
            const h = Object.assign({}, state.currentHand);
            h.street = "river";
            h.pot = 60.0;
            h.community_cards = [
                { rank: 14, suit: 0 }, // As
                { rank: 13, suit: 2 }, // Kd
                { rank: 2, suit: 3 },  // 2c
                { rank: 11, suit: 1 }, // Jh
                { rank: 8, suit: 0 }   // 8s
            ];

            sendTestEvent({
                type: "CardDealt",
                table_id: state.tableId,
                hand_state: h,
                description: "River dealt: 8♠ (Pot: $60.00)"
            });
        });

        document.getElementById("btnTestBet").addEventListener("click", () => {
            if (!state.currentHand) return;
            const h = Object.assign({}, state.currentHand);
            h.current_bet = 20.0;
            h.pot += 20.0;
            h.seats = h.seats.map(s => s.player_id === "player-1" ? Object.assign({}, s, { current_bet: 20.0, stack: s.stack - 20 }) : s);

            sendTestEvent({
                type: "PlayerAction",
                table_id: state.tableId,
                hand_state: h,
                seat_number: 1,
                player_id: "player-1",
                action: "bet",
                amount: 20.0,
                description: "mamayazareyzil bet $20.00 into $60.00 pot"
            });
        });

        document.getElementById("btnTestHeroTurn").addEventListener("click", () => {
            if (!state.currentHand) return;
            sendTestEvent({
                type: "HeroTurn",
                table_id: state.tableId,
                hand_state: state.currentHand,
                player_id: "player-0",
                description: "Hero turn to act"
            });
        });

        document.getElementById("btnTestEnd").addEventListener("click", () => {
            if (!state.currentHand) return;
            const h = Object.assign({}, state.currentHand);
            h.street = "showdown";

            sendTestEvent({
                type: "HandEnd",
                table_id: state.tableId,
                hand_state: h,
                description: "Hand ended at showdown. Pot awarded to Hero."
            });
        });
    }

    // Initialize Application
    function init() {
        elements.btnConnect.addEventListener("click", () => {
            const newId = elements.tableIdInput.value.trim();
            if (newId) {
                state.tableId = newId;
                connectWS();
            }
        });

        elements.btnPauseResume.addEventListener("click", () => {
            state.isPaused = !state.isPaused;
            elements.btnPauseResume.textContent = state.isPaused ? "Resume Stream" : "Pause Stream";
            elements.btnPauseResume.className = state.isPaused ? "btn btn-primary" : "btn btn-secondary";
            addLogEntry(state.isPaused ? "Stream paused." : "Stream resumed.", "info");
        });

        setupTestTriggers();
        renderSeats([], "player-0", 0);
        connectWS();
    }

    // Start on DOM Content Loaded
    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
