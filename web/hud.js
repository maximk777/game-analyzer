/**
 * POKER RTA HUD // High-Performance Live Assistant Co-Pilot
 * Automatic Universal WebSocket Sync, Real-time Equity, EV Advisor & Sizing
 */

(function () {
    "use strict";

    const state = {
        tableId: "coinpoker-live",
        ws: null,
        reconnectTimer: null,
        currentHand: null,
        currentAdvice: null,
    };

    const elements = {
        hudWidget: document.getElementById("hudWidget"),
        hudStatusDot: document.getElementById("hudStatusDot"),
        hudStatusText: document.getElementById("hudStatusText"),
        hudStreetBadge: document.getElementById("hudStreetBadge"),
        hudPotBadge: document.getElementById("hudPotBadge"),
        
        hudHoleCards: document.getElementById("hudHoleCards"),
        hudHandRank: document.getElementById("hudHandRank"),
        hudBoardCards: document.getElementById("hudBoardCards"),
        hudBoardInfo: document.getElementById("hudBoardInfo"),

        hudRecCard: document.getElementById("hudRecCard"),
        hudEvBadge: document.getElementById("hudEvBadge"),
        hudActionType: document.getElementById("hudActionType"),
        hudActionAmount: document.getElementById("hudActionAmount"),
        hudEdgeCallout: document.getElementById("hudEdgeCallout"),
        hudEdgeText: document.getElementById("hudEdgeText"),

        hudEquityVal: document.getElementById("hudEquityVal"),
        hudEquityBar: document.getElementById("hudEquityBar"),
        hudPotOddsVal: document.getElementById("hudPotOddsVal"),
        hudPotOddsBar: document.getElementById("hudPotOddsBar"),

        sizeAmtMin: document.getElementById("sizeAmtMin"),
        sizeAmt25x: document.getElementById("sizeAmt25x"),
        sizeAmt33: document.getElementById("sizeAmt33"),
        sizeAmt66: document.getElementById("sizeAmt66"),
        sizeAmtPot: document.getElementById("sizeAmtPot"),
        sizeAmtAllIn: document.getElementById("sizeAmtAllIn"),

        hudReasoningText: document.getElementById("hudReasoningText"),
        playerCount: document.getElementById("playerCount"),
        hudOpponentsList: document.getElementById("hudOpponentsList"),
        btnToggleOpponents: document.getElementById("btnToggleOpponents"),
        oppToggleIcon: document.getElementById("oppToggleIcon"),
    };

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

    function parseCardData(card) {
        if (!card) return null;
        if (typeof card === "string") {
            if (card.length < 2) return null;
            const r = card.slice(0, -1).toUpperCase();
            const s = card.slice(-1).toLowerCase();
            return {
                rank: r === "T" ? "10" : r,
                suit: s,
                suitClass: SUIT_CLASSES[s] || "suit-s",
                symbol: SUIT_SYMBOLS[s] || "♠"
            };
        }
        if (typeof card === "object") {
            const r = card.rank > 0 ? (card.rank === 10 ? "10" : (card.rank === 14 ? "A" : (card.rank === 13 ? "K" : (card.rank === 12 ? "Q" : (card.rank === 11 ? "J" : String(card.rank)))))) : "?";
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

    function formatChips(val) {
        if (typeof val !== "number" || isNaN(val) || val <= 0) return "$0";
        if (val >= 1000000) return `$${(val / 1000000).toFixed(2)}M`;
        if (val >= 1000) return `$${(val / 1000).toFixed(1)}k`;
        return `$${Math.round(val)}`;
    }

    // Connect WebSocket to live stream
    function connectWebSocket() {
        if (state.ws) {
            try { state.ws.close(); } catch (e) {}
            state.ws = null;
        }

        clearTimeout(state.reconnectTimer);
        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const host = window.location.host || "localhost:8080";
        const wsUrl = `${protocol}//${host}/ws/tables/coinpoker-live`;

        elements.hudStatusText.textContent = "○ Connecting...";
        elements.hudStatusDot.className = "status-dot dot-searching";

        try {
            state.ws = new WebSocket(wsUrl);

            state.ws.onopen = function () {
                elements.hudStatusText.textContent = "● Live · CoinPoker";
                elements.hudStatusDot.className = "status-dot dot-live";
            };

            state.ws.onmessage = function (event) {
                try {
                    const msg = JSON.parse(event.data);
                    handleWSMessage(msg);
                } catch (err) {
                    console.error("WS Parse error:", err);
                }
            };

            state.ws.onclose = function () {
                elements.hudStatusText.textContent = "● Reconnecting...";
                elements.hudStatusDot.className = "status-dot dot-searching";
                scheduleReconnect();
            };

            state.ws.onerror = function () {
                state.ws.close();
            };
        } catch (err) {
            scheduleReconnect();
        }
    }

    function scheduleReconnect() {
        clearTimeout(state.reconnectTimer);
        state.reconnectTimer = setTimeout(connectWebSocket, 1500);
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

    function renderHandState(handState) {
        state.currentHand = handState;
        if (!handState) return;

        // 1. Street & Pot
        const street = (handState.street || "preflop").toLowerCase();
        elements.hudStreetBadge.className = `hud-street-badge street-${street}`;
        elements.hudStreetBadge.textContent = street.toUpperCase();
        elements.hudPotBadge.textContent = `Pot: ${formatChips(handState.pot || 0)}`;

        // 2. Hero Cards
        const parsedHero = [];
        if (handState.hero_cards && handState.hero_cards.length >= 2) {
            for (let i = 0; i < 2; i++) {
                const c = parseCardData(handState.hero_cards[i]);
                if (c && c.rank !== "?") parsedHero.push(c);
            }
        }

        const heroSlots = elements.hudHoleCards.querySelectorAll(".hud-card");
        if (parsedHero.length === 2) {
            heroSlots.forEach((slot, idx) => {
                const c = parsedHero[idx];
                slot.className = `hud-card ${c.suitClass}`;
                slot.querySelector(".card-rank").textContent = c.rank;
                slot.querySelector(".card-suit").textContent = c.symbol;
            });
            elements.hudHandRank.textContent = handState.hero_made_hand || "Pocket Cards";
        } else {
            heroSlots.forEach(slot => {
                slot.className = "hud-card card-empty";
                slot.querySelector(".card-rank").textContent = "?";
                slot.querySelector(".card-suit").textContent = "";
            });
            elements.hudHandRank.textContent = handState.hero_made_hand || (handState.seats && handState.seats.length > 0 ? "Spectator / Observ. Mode" : "Awaiting Deal...");
        }

        // 3. Board Cards
        const parsedBoard = [];
        if (handState.community_cards && Array.isArray(handState.community_cards)) {
            handState.community_cards.forEach(raw => {
                const c = parseCardData(raw);
                if (c && c.rank !== "?") parsedBoard.push(c);
            });
        }

        const boardSlots = elements.hudBoardCards.querySelectorAll(".board-card");
        boardSlots.forEach((slot, idx) => {
            if (idx < parsedBoard.length) {
                const c = parsedBoard[idx];
                slot.className = `board-card ${c.suitClass}`;
                slot.textContent = `${c.rank}${c.symbol}`;
            } else {
                slot.className = "board-card empty";
                slot.textContent = "_";
            }
        });

        if (parsedBoard.length > 0) {
            elements.hudBoardInfo.textContent = `${street.toUpperCase()} (${parsedBoard.length} cards)`;
        } else {
            elements.hudBoardInfo.textContent = "Preflop (No Board Cards)";
        }

        // 4. Sizing Matrix Calculation
        updateSizingGrid(handState);

        // 5. Seated Players
        renderPlayers(handState.seats || []);
    }

    function renderAdvisorRecommendation(rec) {
        state.currentAdvice = rec;
        if (!rec) return;

        const act = (rec.primary_action || "check").toLowerCase();
        elements.hudActionType.textContent = (rec.primary_action || "CHECK").toUpperCase();
        
        if (rec.recommended_amount && rec.recommended_amount > 0) {
            elements.hudActionAmount.textContent = formatChips(rec.recommended_amount);
        } else {
            elements.hudActionAmount.textContent = "";
        }

        // Card styling by action
        elements.hudRecCard.className = "hud-recommendation-card";
        if (act.includes("raise") || act.includes("bet")) {
            elements.hudRecCard.classList.add("rec-raise");
        } else if (act.includes("fold")) {
            elements.hudRecCard.classList.add("rec-fold");
        } else if (act.includes("call") || act.includes("check")) {
            elements.hudRecCard.classList.add("rec-call");
        } else if (act.includes("all")) {
            elements.hudRecCard.classList.add("rec-allin");
        }

        // EV Badge
        const evVal = rec.ev || (rec.actions && rec.actions[0] ? rec.actions[0].ev : 0.0);
        elements.hudEvBadge.textContent = `EV: ${evVal >= 0 ? "+" : ""}$${evVal.toFixed(2)}`;

        // Equity & Pot Odds Gauges
        const eq = Math.round((rec.equity || 0) * 1000) / 10;
        const po = Math.round((rec.pot_odds || 0) * 1000) / 10;
        elements.hudEquityVal.textContent = `${eq.toFixed(1)}%`;
        elements.hudEquityBar.style.width = `${Math.min(eq, 100)}%`;
        elements.hudPotOddsVal.textContent = `${po.toFixed(1)}%`;
        elements.hudPotOddsBar.style.width = `${Math.min(po, 100)}%`;

        // Edge Callout
        const edge = eq - po;
        if (edge > 10) {
            elements.hudEdgeText.textContent = `🔥 +${edge.toFixed(1)}% Value Edge over Pot Odds`;
            elements.hudEdgeCallout.style.color = "var(--accent-green)";
        } else if (edge > 0) {
            elements.hudEdgeText.textContent = `⚖ +${edge.toFixed(1)}% Marginal Positive Expectation`;
            elements.hudEdgeCallout.style.color = "var(--accent-cyan)";
        } else {
            elements.hudEdgeText.textContent = `⚠️ Low Equity (${eq.toFixed(1)}% < ${po.toFixed(1)}% required)`;
            elements.hudEdgeCallout.style.color = "var(--accent-red)";
        }

        // Reasoning Text
        if (rec.reasoning) {
            elements.hudReasoningText.textContent = rec.reasoning;
        }
    }

    function updateSizingGrid(handState) {
        const pot = handState.pot || 1000;
        const curBet = handState.current_bet || 0;
        const minRaise = handState.min_raise || curBet * 2 || 2000;

        elements.sizeAmtMin.textContent = formatChips(minRaise);
        elements.sizeAmt25x.textContent = formatChips(Math.max(curBet * 2.5, minRaise));
        elements.sizeAmt33.textContent = formatChips(curBet + pot * 0.33);
        elements.sizeAmt66.textContent = formatChips(curBet + pot * 0.66);
        elements.sizeAmtPot.textContent = formatChips(curBet + pot);
        elements.sizeAmtAllIn.textContent = "All-In";
    }

    function renderPlayers(seats) {
        elements.playerCount.textContent = seats.length;
        if (!seats || seats.length === 0) {
            elements.hudOpponentsList.innerHTML = '<div class="empty-opp">No player data yet.</div>';
            return;
        }

        let html = "";
        seats.forEach(s => {
            html += `
                <div class="opp-row">
                    <span class="opp-name">${s.player_name || "Player"}</span>
                    <span class="opp-stack">${s.stack > 0 ? formatChips(s.stack) : "Active"}</span>
                </div>
            `;
        });
        elements.hudOpponentsList.innerHTML = html;
    }

    // Toggle Seated Players Drawer
    if (elements.btnToggleOpponents) {
        elements.btnToggleOpponents.addEventListener("click", () => {
            const list = elements.hudOpponentsList;
            if (list.style.display === "none") {
                list.style.display = "flex";
                elements.oppToggleIcon.textContent = "▲";
            } else {
                list.style.display = "none";
                elements.oppToggleIcon.textContent = "▼";
            }
        });
    }

    // Start WebSocket
    connectWebSocket();
})();
