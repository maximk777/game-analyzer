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
        // The last explanation given, kept after the spot it belonged to is
        // over. Most of the time there is no decision in front of hero, and a
        // panel that blanks itself the moment they act is a panel whose
        // reasoning can only be read in the half-second they are acting. It is
        // marked as past rather than shown as current.
        lastReasoning: "",
        lastReasoningSpot: "",
        lastCoach: null,
        lastCoachSpot: "",
    };

    // spotLabel names the decision an explanation belonged to, e.g. "ФЛОП · 12".
    function spotLabel(hand) {
        if (!hand) return "";
        const street = streetRU((hand.street || "").toLowerCase());
        const id = String(hand.hand_id || "");
        const tail = id.length > 4 ? id.slice(-4) : id;
        return tail ? `${street} · ${tail}` : street;
    }

    const elements = {
        hudWidget: document.getElementById("hudWidget"),
        hudStatusDot: document.getElementById("hudStatusDot"),
        hudStatusText: document.getElementById("hudStatusText"),
        hudPhaseBadge: document.getElementById("hudPhaseBadge"),
        hudStreetBadge: document.getElementById("hudStreetBadge"),
        hudPotBadge: document.getElementById("hudPotBadge"),

        hudCoachCard: document.getElementById("hudCoachCard"),
        hudCoachVerdict: document.getElementById("hudCoachVerdict"),
        hudCoachAction: document.getElementById("hudCoachAction"),
        hudCoachAmount: document.getElementById("hudCoachAmount"),
        hudCoachText: document.getElementById("hudCoachText"),
        hudCoachBluff: document.getElementById("hudCoachBluff"),
        hudCoachBluffAmt: document.getElementById("hudCoachBluffAmt"),
        hudCoachBluffText: document.getElementById("hudCoachBluffText"),
        hudCoachOpinion: document.getElementById("hudCoachOpinion"),
        hudCoachModel: document.getElementById("hudCoachModel"),
        
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
        hudRiskCard: document.getElementById("hudRiskCard"),
        hudRiskHeroHand: document.getElementById("hudRiskHeroHand"),
        hudRiskBehind: document.getElementById("hudRiskBehind"),
        hudRiskList: document.getElementById("hudRiskList"),
        hudRiskCard: document.getElementById("hudRiskCard"),
        hudRiskHeroHand: document.getElementById("hudRiskHeroHand"),
        hudRiskBehind: document.getElementById("hudRiskBehind"),
        hudRiskList: document.getElementById("hudRiskList"),

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

    // Money, printed at whatever precision the amount actually has.
    //
    // This used to round to the nearest whole chip below a thousand, which is
    // right for a table playing 1K/2K and wrong for every micro table: a pot of
    // 0.02 printed as "$0". And not only the pot -- the same formatter prints
    // the recommended amount, every bet size and every opponent stack, so on a
    // 0.01/0.02 table the entire HUD read zero while the numbers behind it were
    // correct all along.
    function formatChips(val) {
        if (typeof val !== "number" || isNaN(val) || val <= 0) return "$0";
        if (val >= 1000000) return `$${(val / 1000000).toFixed(2)}M`;
        if (val >= 1000) return `$${(val / 1000).toFixed(1)}k`;
        if (Number.isInteger(val)) return `$${val}`;

        // Four places below a unit, two above it: enough for a 0.0025 ante and
        // for a 12.75 stack, without printing noise on either.
        let text = val.toFixed(val < 1 ? 4 : 2);
        if (text.includes(".")) {
            text = text.replace(/0+$/, "").replace(/\.$/, "");
            // Money keeps two places once it has any, so a big blind reads
            // "$0.10" rather than "$0.1"; a sub-cent ante keeps what it needs.
            const dot = text.indexOf(".");
            if (dot >= 0 && text.length - dot - 1 < 2) {
                text = Number(text).toFixed(2);
            }
        }
        // Smaller than the smallest place shown. Saying "$0" here would be the
        // bug all over again, so say what it is instead.
        if (text === "0" || text === "") return `$${val.toPrecision(2)}`;
        return `$${text}`;
    }

    // Connect WebSocket to live stream

    // Russian labels for streets and actions, to match the HUD design.
    const STREET_RU = { preflop: "ПРЕФЛОП", flop: "ФЛОП", turn: "ТЁРН", river: "РИВЕР", showdown: "ВСКРЫТИЕ" };
    const ACTION_RU = { fold: "ФОЛД", check: "ЧЕК", call: "КОЛЛ", bet: "БЕТ", raise: "РЕЙЗ", all_in: "ОЛ-ИН", "all-in": "ОЛ-ИН", allin: "ОЛ-ИН" };
    function streetRU(s) { return STREET_RU[(s || "").toLowerCase()] || (s || "").toUpperCase(); }
    function actionRU(a) { return ACTION_RU[(a || "").toLowerCase().replace(/\s+/g, "-")] || (a || "").toUpperCase(); }

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
                // A null payload means "no advice for this state". It must be
                // rendered, not ignored: dropping it left the previous hand's
                // recommendation on screen looking current.
                renderAdvisorRecommendation(msg.payload || null, msg.reason || "");
                break;
            case "coach":
                renderCoach(msg.payload || null);
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
        elements.hudStreetBadge.textContent = streetRU(street);
        elements.hudPotBadge.textContent = `Банк ${formatChips(handState.pot || 0)}`;

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
            elements.hudBoardInfo.textContent = `${streetRU(street)} · ${parsedBoard.length}`;
        } else {
            elements.hudBoardInfo.textContent = "Preflop (No Board Cards)";
        }

        // 4. Sizing Matrix Calculation
        updateSizingGrid(handState);

        // 5. Seated Players
        renderPlayers(handState.seats || []);
    }

    function clearAdvisorRecommendation(reason) {
        elements.hudActionType.textContent = "—";
        elements.hudActionAmount.textContent = "";
        elements.hudRecCard.className = "hud-recommendation-card rec-idle";
        elements.hudEvBadge.textContent = "EV: —";
        elements.hudEquityVal.textContent = "—";
        elements.hudEquityBar.style.width = "0%";
        elements.hudPotOddsVal.textContent = "—";
        elements.hudPotOddsBar.style.width = "0%";
        // Say which of the reasons it is. One message for every case was the
        // wrong message more often than not: at a showdown, with hero's cards
        // in the panel above, it read "no hero cards" -- which looks like the
        // tool is broken rather than like a hand that is over.
        elements.hudEdgeText.textContent = reason || "Совет не считается";
        elements.hudEdgeCallout.style.color = "var(--text-muted)";

        // The reasoning stays. Between hero's own decisions there is nothing to
        // advise, and that is exactly when there is time to read why the last
        // advice was what it was -- so the last explanation is kept and labelled
        // with the spot it came from, instead of being replaced by a line about
        // waiting.
        if (state.lastReasoning) {
            elements.hudReasoningText.classList.add("strategy-past");
            elements.hudReasoningText.textContent =
                `${state.lastReasoningSpot ? state.lastReasoningSpot + " · " : ""}` +
                `прошлый совет: ${state.lastReasoning}`;
        } else {
            elements.hudReasoningText.classList.remove("strategy-past");
            elements.hudReasoningText.textContent = reason
                ? reason + "."
                : "Ожидание раздачи с видимыми карманными картами.";
        }
        // The second opinion likewise: kept, dimmed, and marked as past.
        markCoachPast();
    }

    function renderAdvisorRecommendation(rec, reason) {
        state.currentAdvice = rec;
        if (!rec) {
            clearAdvisorRecommendation(reason);
            return;
        }

        // How well the table is understood, and therefore how hard the tool is
        // playing. A user needs to see this: the same hand is advised
        // differently against a table of strangers and a table it has watched
        // for two hundred hands, and without the badge that difference looks
        // like the tool changing its mind.
        if (elements.hudPhaseBadge) {
            const phase = rec.phase || "разведка";
            const known = Math.round((rec.table_knowledge || 0) * 100);
            const cls = phase === "давление" ? "phase-press"
                : phase === "применение" ? "phase-apply" : "phase-scout";
            elements.hudPhaseBadge.className = `hud-phase-badge ${cls}`;
            elements.hudPhaseBadge.textContent = `${phase} ${known}%`;
            elements.hudPhaseBadge.title =
                `Стол изучен на ${known}% по наименее известному сопернику. ` +
                `Пока он не изучен, крупные ставки против него оцениваются с запасом.`;
        }

        const act = (rec.primary_action || "check").toLowerCase();
        elements.hudActionType.textContent = actionRU(rec.primary_action || "check");
        
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

        renderRisk(rec.risk);

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

        // Reasoning Text. Kept as the last explanation, so it can still be read
        // once the spot is over -- see clearAdvisorRecommendation.
        if (rec.reasoning) {
            elements.hudReasoningText.classList.remove("strategy-past");
            elements.hudReasoningText.textContent = rec.reasoning;
            state.lastReasoning = rec.reasoning;
            state.lastReasoningSpot = spotLabel(state.currentHand);
        }

        // Advice built on the equilibrium baseline rather than on observed
        // tendencies must say so, or a guess reads like a measurement.
        if (rec.has_reads === false) {
            elements.hudRecCard.classList.add("rec-no-reads");
        }
    }

    // What beats hero right now, and how much of an opponent's range holds it.
    //
    // The counts arrive exact -- every combination in the range is played
    // against the board -- so this can name hands and numbers plainly. It
    // exists because a single equity figure hides the shape of the losses:
    // kings on 9-9-7-5-Q win 88 hands in 100 and are dead against the queens,
    // and the percentage alone never says a full house is live.
    const RISK_NAMES = {
        "High Card": "старшая карта",
        "One Pair": "пара",
        "Two Pair": "две пары",
        "Three of a Kind": "тройка",
        "Straight": "стрит",
        "Flush": "флеш",
        "Full House": "фулл-хаус",
        "Four of a Kind": "каре",
        "Straight Flush": "стрит-флеш",
    };

    function renderRisk(risk) {
        const card = elements.hudRiskCard;
        if (!card) return;

        if (!risk || !risk.combos || !risk.beaten_by || risk.beaten_by.length === 0) {
            card.hidden = true;
            return;
        }
        card.hidden = false;

        const behind = (risk.behind || 0) * 100;
        elements.hudRiskHeroHand.textContent = RISK_NAMES[risk.hero_hand] || risk.hero_hand || "—";
        elements.hudRiskBehind.textContent = `бьют ${behind.toFixed(0)} из 100`;
        elements.hudRiskBehind.className = "risk-behind " +
            (behind >= 20 ? "risk-high" : behind >= 8 ? "risk-mid" : "risk-low");

        elements.hudRiskList.innerHTML = risk.beaten_by.slice(0, 4).map((c) => {
            let name = RISK_NAMES[c.category] || c.category;
            // The same hand you hold beats you by its kicker, and naming it
            // plainly reads as though something rarer were needed.
            if (c.category === risk.hero_hand) name += " со старшим киккером";
            const pct = (c.share || 0) * 100;
            // Under half a percent, a rounded figure would read as zero and
            // look like a bug rather than a rarity.
            const shown = pct < 0.5 ? "<1" : pct.toFixed(0);
            return `<li><span class="risk-name">${name}</span>` +
                `<span class="risk-share">${shown} из 100</span>` +
                `<span class="risk-combos">${c.combos}</span></li>`;
        }).join("");
    }

    // Hero's own stack, from the seats. Everything offered has to fit inside it.
    function heroStack(handState) {
        const heroID = handState.hero_id;
        for (const s of handState.seats || []) {
            if (s.player_id && s.player_id === heroID && s.stack > 0) return s.stack;
        }
        return 0;
    }

    function updateSizingGrid(handState) {
        const pot = handState.pot || 0;
        const curBet = handState.current_bet || 0;
        const minRaise = handState.min_raise || curBet * 2 || 0;

        // Nothing above the stack. This grid recomputes sizes from the pot,
        // beside an advisor that caps every size it offers at the effective
        // stack -- so the two disagreed, and the grid won because it is what a
        // player reads. Live, with 71,040 behind, it offered 168.2k, 334.5k and
        // 505.8k: three sizes that cannot be bet.
        const stack = heroStack(handState);
        const fits = (v) => (stack > 0 ? Math.min(v, stack) : v);

        // With no pot read there is no scale, and a fabricated one is worse
        // than a blank: these fell back to a pot of 1000 and a minimum raise of
        // 2000, which are the chips at 1K/2K and a hundred thousand big blinds
        // at 0.01/0.02. The grid showed confident sizes for a table it had not
        // read. A dash says what is true.
        const dash = "—";
        elements.sizeAmtMin.textContent = minRaise > 0 ? formatChips(fits(minRaise)) : dash;
        elements.sizeAmt25x.textContent = curBet > 0 || minRaise > 0
            ? formatChips(fits(Math.max(curBet * 2.5, minRaise))) : dash;
        elements.sizeAmt33.textContent = pot > 0 ? formatChips(fits(curBet + pot * 0.33)) : dash;
        elements.sizeAmt66.textContent = pot > 0 ? formatChips(fits(curBet + pot * 0.66)) : dash;
        elements.sizeAmtPot.textContent = pot > 0 ? formatChips(fits(curBet + pot)) : dash;
        elements.sizeAmtAllIn.textContent = "All-In";
    }

    // The model's second opinion. It arrives seconds after the state it is
    // about, so it has its own message and its own card.
    //
    // The card appears only once there is something to say. A model that is
    // switched off leaves no empty box behind, and a spot the model is still
    // reading says so rather than showing the previous spot's answer.
    // markCoachPast keeps the last second opinion on screen and says it is the
    // last one rather than the current one. It is the only way the model's words
    // can be read at all: the answer arrives while hero is acting and the spot is
    // over a second later.
    function markCoachPast() {
        if (!state.lastCoach) {
            elements.hudCoachCard.hidden = true;
            return;
        }
        elements.hudCoachCard.hidden = false;
        elements.hudCoachCard.classList.add("coach-past");
        const a = state.lastCoach.advice || {};
        const model = a.model || "модель";
        elements.hudCoachModel.textContent =
            `${model} · ${state.lastCoachSpot ? state.lastCoachSpot + " · " : ""}прошлый ход`;
    }

    function renderCoach(update) {
        // An empty update is "there is no decision to have a second opinion
        // about" -- the last answer stays, marked as past, so it can be read
        // between hands instead of vanishing with the spot.
        if (!update || (!update.spot && !update.error)) {
            markCoachPast();
            return;
        }
        elements.hudCoachCard.hidden = false;
        elements.hudCoachCard.classList.remove("coach-past");

        // Bluff and opinion belong to a concrete answer; hide them until one
        // arrives, so a pending or failed read shows no stale flag.
        elements.hudCoachBluff.hidden = true;
        elements.hudCoachOpinion.hidden = true;

        if (update.error) {
            elements.hudCoachVerdict.className = "coach-verdict failed";
            elements.hudCoachVerdict.textContent = "НЕ ОТВЕТИЛА";
            elements.hudCoachAction.textContent = "—";
            elements.hudCoachAmount.textContent = "";
            elements.hudCoachText.textContent = update.error;
            return;
        }

        if (update.pending || !update.advice) {
            elements.hudCoachVerdict.className = "coach-verdict waiting";
            elements.hudCoachVerdict.textContent = "ЧИТАЕТ…";
            elements.hudCoachAction.textContent = "—";
            elements.hudCoachAmount.textContent = "";
            elements.hudCoachText.textContent = "Модель читает стол.";
            return;
        }

        const a = update.advice;
        // Kept as the last answer, for reading once the spot is over.
        state.lastCoach = update;
        state.lastCoachSpot = spotLabel(state.currentHand);
        // Agreement is the boring case. A disagreement is the only thing on
        // this card worth stopping at, so it is the one that is marked.
        elements.hudCoachVerdict.className = `coach-verdict ${a.agrees ? "agrees" : "differs"}`;
        elements.hudCoachVerdict.textContent = a.agrees ? "СОГЛАСНА" : "НЕ СОГЛАСНА";

        elements.hudCoachAction.textContent = a.action ? actionRU(a.action) : "—";
        elements.hudCoachAmount.textContent = a.amount > 0 ? formatChips(a.amount) : "";
        elements.hudCoachText.textContent = a.reasoning || "";

        // Bluff spot: only when the model flags one. The amount is the bluff
        // size -- the model's own recommended bet when it is bluffing -- shown
        // large so "there is a bluff here, this big" reads at a glance.
        if (a.bluff) {
            elements.hudCoachBluffAmt.textContent = a.amount > 0 ? formatChips(a.amount) : "";
            elements.hudCoachBluffText.textContent = a.bluff_reason || "";
            elements.hudCoachBluff.hidden = false;
        }

        // The model's own opinion, quoted in its own words.
        if (a.opinion) {
            elements.hudCoachOpinion.textContent = `«${a.opinion}»`;
            elements.hudCoachOpinion.hidden = false;
        }

        const conf = typeof a.confidence === "number" && a.confidence > 0
            ? ` · уверенность ${Math.round(a.confidence * 100)}%`
            : "";
        elements.hudCoachModel.textContent = `${a.model || "модель"}${conf}`;
    }

    // Accumulated stats per player, fetched once from the profile endpoint and
    // kept for the session. The wire carries the site's own session VPIP on the
    // seat, which fills the gap until our own sample exists.
    const profileCache = {};
    const profileFetchedAt = {};
    const profilePending = {};
    const PROFILE_TTL_MS = 20000; // stats accumulate as hands play; refresh occasionally
    // Below this many hands our own frequencies are noise, and the site's own
    // month-long aggregate describes the same player better. Matches
    // advice.ThinSample.
    const OWN_SAMPLE_MIN = 25;
    let lastSeats = [];

    // fetchProfile pulls a player's accumulated stats, then re-renders so the
    // numbers appear as they arrive without blocking the frame. It refetches
    // once the cached copy is older than the TTL, so a player's stats grow with
    // the session rather than freezing at what they were when first seen.
    function fetchProfile(playerID) {
        if (!playerID || profilePending[playerID]) return;
        // Skip if we attempted within the TTL, whether or not it yielded stats.
        if (profileFetchedAt[playerID] && Date.now() - profileFetchedAt[playerID] < PROFILE_TTL_MS) return;
        profilePending[playerID] = true;
        fetch(`/api/v1/players/${encodeURIComponent(playerID)}/profile`)
            .then((r) => (r.ok ? r.json() : null))
            .then((data) => {
                if (data && data.stats) profileCache[playerID] = data.stats;
            })
            .catch(() => {})
            .finally(() => {
                // Stamp on every outcome so a player with no stats yet is not
                // refetched on every re-render -- only once per TTL.
                profileFetchedAt[playerID] = Date.now();
                delete profilePending[playerID];
                renderPlayers(lastSeats);
            });
    }

    // POS_CLASS tints the position chip so seat order reads at a glance.
    const POS_CLASS = { BTN: "pos-btn", SB: "pos-sb", BB: "pos-bb", CO: "pos-co", MP: "pos-mp", UTG: "pos-utg" };

    // vpipClass is a tightness cue: loose is a target, tight is a warning.
    function vpipClass(v) {
        return v >= 40 ? "stat-loose" : v >= 25 ? "stat-mid" : "stat-tight";
    }

    function renderPlayers(seats) {
        lastSeats = seats || [];
        const heroID = (state.currentHand && state.currentHand.hero_id) || "";
        elements.playerCount.textContent = lastSeats.length;
        if (lastSeats.length === 0) {
            elements.hudOpponentsList.innerHTML = '<div class="empty-opp">Нет данных об игроках.</div>';
            return;
        }

        let html = "";
        lastSeats.forEach((s) => {
            const isHero = s.player_id && s.player_id === heroID;
            const pos = s.position || "";
            const posClass = POS_CLASS[pos] || "pos-none";
            const stack = s.stack > 0 ? formatChips(s.stack) : "—";
            const folded = s.is_folded ? " opp-folded" : "";

            let statsHtml;
            if (isHero) {
                statsHtml = '<span class="opp-you">ВЫ</span>';
            } else {
                const st = profileCache[s.player_id];
                const site = s.site_stats;
                if (st && st.hands_count >= OWN_SAMPLE_MIN) {
                    // Our own accumulated sample: VPIP/PFR/3bet.
                    statsHtml =
                        `<span class="opp-stat ${vpipClass(st.vpip)}">${Math.round(st.vpip)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round(st.pfr)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round(st.three_bet)}</span>` +
                        `<span class="opp-hands">${formatCount(st.hands_count)}</span>`;
                    fetchProfile(s.player_id); // keep it fresh across hands
                } else if (site && site.vpip > 0) {
                    // The site's own aggregate, marked with a degree sign. Its
                    // hand count is shown when the pool reports one -- the
                    // real-money pool does not, and "30д" says what it covers.
                    const v = site.vpip * 100;
                    statsHtml =
                        `<span class="opp-stat ${vpipClass(v)}">${Math.round(v)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round((site.pfr || 0) * 100)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round((site.three_bet || 0) * 100)}` +
                        `<span class="opp-stat-sfx">°</span></span>` +
                        `<span class="opp-hands">${site.hands > 0 ? formatCount(site.hands) : "30д"}</span>`;
                    fetchProfile(s.player_id);
                } else if (st && st.hands_count > 0) {
                    // Our own thin sample, better than nothing and marked as
                    // small by the count beside it.
                    statsHtml =
                        `<span class="opp-stat ${vpipClass(st.vpip)}">${Math.round(st.vpip)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round(st.pfr)}` +
                        `<span class="opp-stat-sep">/</span>${Math.round(st.three_bet)}</span>` +
                        `<span class="opp-hands">${formatCount(st.hands_count)}</span>`;
                    fetchProfile(s.player_id);
                } else if (s.server_vpip > 0) {
                    // No sample yet: the site's own session VPIP, marked.
                    statsHtml =
                        `<span class="opp-stat ${vpipClass(s.server_vpip)}">${Math.round(s.server_vpip)}` +
                        `<span class="opp-stat-sfx">*</span></span>` +
                        `<span class="opp-hands">${formatCount(s.server_hands || 0)}</span>`;
                    fetchProfile(s.player_id);
                } else {
                    statsHtml = '<span class="opp-stat stat-none">—</span>';
                    fetchProfile(s.player_id);
                }
            }

            html +=
                `<div class="opp-row${folded}">` +
                `<span class="opp-pos ${posClass}">${pos || "·"}</span>` +
                `<span class="opp-name">${escapeHtml(s.player_name || "Player")}</span>` +
                `<span class="opp-stack">${stack}</span>` +
                statsHtml +
                `</div>`;
        });
        elements.hudOpponentsList.innerHTML = html;
    }

    // formatCount abbreviates a hands count: 1600 -> "1.6К".
    function formatCount(n) {
        if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "К";
        return String(n);
    }

    function escapeHtml(s) {
        return String(s).replace(/[&<>"']/g, (c) =>
            ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
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
