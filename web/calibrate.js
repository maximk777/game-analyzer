/**
 * POKER RTA // ROI Calibrator & Vision Visualizer
 * Interactive Canvas Bounding Box Editor & Server Sync
 */

(function () {
    "use strict";

    // Default 6-Max Normalized ROI Config (mirrors pkg/vision/roi.go)
    function getDefaultROIConfig() {
        return {
            hero_cards: [
                { x: 0.440, y: 0.740, width: 0.055, height: 0.110 },
                { x: 0.505, y: 0.740, width: 0.055, height: 0.110 }
            ],
            community_cards: [
                { x: 0.285, y: 0.410, width: 0.058, height: 0.105 },
                { x: 0.355, y: 0.410, width: 0.058, height: 0.105 },
                { x: 0.425, y: 0.410, width: 0.058, height: 0.105 },
                { x: 0.495, y: 0.410, width: 0.058, height: 0.105 },
                { x: 0.565, y: 0.410, width: 0.058, height: 0.105 }
            ],
            pot: { x: 0.410, y: 0.320, width: 0.180, height: 0.050 },
            action_buttons: { x: 0.650, y: 0.880, width: 0.320, height: 0.090 },
            timer_bar: { x: 0.420, y: 0.710, width: 0.160, height: 0.015 },
            seats: [
                {
                    seat_number: 0,
                    is_hero: true,
                    avatar: { x: 0.440, y: 0.860, width: 0.120, height: 0.070 },
                    nameplate: { x: 0.440, y: 0.935, width: 0.120, height: 0.035 },
                    stack: { x: 0.440, y: 0.970, width: 0.120, height: 0.028 },
                    bet: { x: 0.450, y: 0.680, width: 0.100, height: 0.040 },
                    cards: [
                        { x: 0.440, y: 0.740, width: 0.055, height: 0.110 },
                        { x: 0.505, y: 0.740, width: 0.055, height: 0.110 }
                    ],
                    active_badge: { x: 0.400, y: 0.720, width: 0.035, height: 0.035 }
                },
                {
                    seat_number: 1,
                    is_hero: false,
                    avatar: { x: 0.100, y: 0.640, width: 0.110, height: 0.070 },
                    nameplate: { x: 0.100, y: 0.715, width: 0.110, height: 0.035 },
                    stack: { x: 0.100, y: 0.750, width: 0.110, height: 0.028 },
                    bet: { x: 0.220, y: 0.620, width: 0.090, height: 0.035 },
                    cards: [
                        { x: 0.120, y: 0.560, width: 0.040, height: 0.070 },
                        { x: 0.165, y: 0.560, width: 0.040, height: 0.070 }
                    ],
                    active_badge: { x: 0.220, y: 0.660, width: 0.030, height: 0.030 }
                },
                {
                    seat_number: 2,
                    is_hero: false,
                    avatar: { x: 0.120, y: 0.180, width: 0.110, height: 0.070 },
                    nameplate: { x: 0.120, y: 0.255, width: 0.110, height: 0.035 },
                    stack: { x: 0.120, y: 0.290, width: 0.110, height: 0.028 },
                    bet: { x: 0.240, y: 0.260, width: 0.090, height: 0.035 },
                    cards: [
                        { x: 0.130, y: 0.100, width: 0.040, height: 0.070 },
                        { x: 0.175, y: 0.100, width: 0.040, height: 0.070 }
                    ],
                    active_badge: { x: 0.240, y: 0.220, width: 0.030, height: 0.030 }
                },
                {
                    seat_number: 3,
                    is_hero: false,
                    avatar: { x: 0.440, y: 0.050, width: 0.120, height: 0.070 },
                    nameplate: { x: 0.440, y: 0.125, width: 0.120, height: 0.035 },
                    stack: { x: 0.440, y: 0.160, width: 0.120, height: 0.028 },
                    bet: { x: 0.450, y: 0.220, width: 0.100, height: 0.035 },
                    cards: [
                        { x: 0.445, y: 0.010, width: 0.040, height: 0.070 },
                        { x: 0.490, y: 0.010, width: 0.040, height: 0.070 }
                    ],
                    active_badge: { x: 0.410, y: 0.200, width: 0.030, height: 0.030 }
                },
                {
                    seat_number: 4,
                    is_hero: false,
                    avatar: { x: 0.770, y: 0.180, width: 0.110, height: 0.070 },
                    nameplate: { x: 0.770, y: 0.255, width: 0.110, height: 0.035 },
                    stack: { x: 0.770, y: 0.290, width: 0.110, height: 0.028 },
                    bet: { x: 0.670, y: 0.260, width: 0.090, height: 0.035 },
                    cards: [
                        { x: 0.780, y: 0.100, width: 0.040, height: 0.070 },
                        { x: 0.825, y: 0.100, width: 0.040, height: 0.070 }
                    ],
                    active_badge: { x: 0.730, y: 0.220, width: 0.030, height: 0.030 }
                },
                {
                    seat_number: 5,
                    is_hero: false,
                    avatar: { x: 0.790, y: 0.640, width: 0.110, height: 0.070 },
                    nameplate: { x: 0.790, y: 0.715, width: 0.110, height: 0.035 },
                    stack: { x: 0.790, y: 0.750, width: 0.110, height: 0.028 },
                    bet: { x: 0.690, y: 0.620, width: 0.090, height: 0.035 },
                    cards: [
                        { x: 0.795, y: 0.560, width: 0.040, height: 0.070 },
                        { x: 0.840, y: 0.560, width: 0.040, height: 0.070 }
                    ],
                    active_badge: { x: 0.740, y: 0.660, width: 0.030, height: 0.030 }
                }
            ]
        };
    }

    // Application State
    const state = {
        roi: getDefaultROIConfig(),
        activeKey: "hero_card_0",
        filter: "all",
        zoom: 1.0,
        liveStream: true,
        streamInterval: null,
        currentImage: null,
        frameWidth: 1000,
        frameHeight: 800,
        dragState: null, // { type: 'move'|'nw'|'ne'|'se'|'sw', startX, startY, origBox }
        hoverBox: null,
        hoverHandle: null,
    };

    // DOM Elements
    const elements = {
        windowSelect: document.getElementById("windowSelect"),
        btnRefreshWindows: document.getElementById("btnRefreshWindows"),
        btnToggleLiveStream: document.getElementById("btnToggleLiveStream"),
        streamStatusText: document.getElementById("streamStatusText"),
        btnFetchSnapshot: document.getElementById("btnFetchSnapshot"),
        fileUploadInput: document.getElementById("fileUploadInput"),
        btnLoadROI: document.getElementById("btnLoadROI"),
        btnSaveROI: document.getElementById("btnSaveROI"),
        btnZoomIn: document.getElementById("btnZoomIn"),
        btnZoomOut: document.getElementById("btnZoomOut"),
        btnZoomReset: document.getElementById("btnZoomReset"),
        zoomLevelDisplay: document.getElementById("zoomLevelDisplay"),
        canvasViewport: document.getElementById("canvasViewport"),
        canvasStage: document.getElementById("canvasStage"),
        bgImageCanvas: document.getElementById("bgImageCanvas"),
        roiOverlayCanvas: document.getElementById("roiOverlayCanvas"),
        emptyCanvasState: document.getElementById("emptyCanvasState"),
        btnLoadSampleImage: document.getElementById("btnLoadSampleImage"),
        frameDimDisplay: document.getElementById("frameDimDisplay"),
        cursorPosDisplay: document.getElementById("cursorPosDisplay"),
        selectedBoxName: document.getElementById("selectedBoxName"),
        btnResetDefaults: document.getElementById("btnResetDefaults"),
        regionSelect: document.getElementById("regionSelect"),
        inputX: document.getElementById("inputX"),
        inputY: document.getElementById("inputY"),
        inputWidth: document.getElementById("inputWidth"),
        inputHeight: document.getElementById("inputHeight"),
        nudgeUp: document.getElementById("nudgeUp"),
        nudgeDown: document.getElementById("nudgeDown"),
        nudgeLeft: document.getElementById("nudgeLeft"),
        nudgeRight: document.getElementById("nudgeRight"),
        btnCopyJSON: document.getElementById("btnCopyJSON"),
        btnPasteJSON: document.getElementById("btnPasteJSON"),
        btnDownloadJSON: document.getElementById("btnDownloadJSON"),
        roiJsonTextarea: document.getElementById("roiJsonTextarea"),
        toastBanner: document.getElementById("toastBanner"),
    };

    // Flatten ROI hierarchy into mapped list of boxes
    function getFlattenedBoxes(roi) {
        const boxes = [];

        // Hero Cards
        if (roi.hero_cards) {
            roi.hero_cards.forEach((b, i) => {
                boxes.push({ key: `hero_card_${i}`, label: `Hero Card ${i + 1}`, cat: "hero", color: "#00f2fe", box: b });
            });
        }

        // Community Cards
        if (roi.community_cards) {
            roi.community_cards.forEach((b, i) => {
                const names = ["Flop 1", "Flop 2", "Flop 3", "Turn", "River"];
                boxes.push({ key: `comm_card_${i}`, label: names[i] || `Board ${i + 1}`, cat: "community", color: "#10b981", box: b });
            });
        }

        // Pot
        if (roi.pot) {
            boxes.push({ key: "pot", label: "Pot Area", cat: "pot", color: "#f59e0b", box: roi.pot });
        }

        // Action Buttons
        if (roi.action_buttons) {
            boxes.push({ key: "action_buttons", label: "Action Buttons", cat: "actions", color: "#f97316", box: roi.action_buttons });
        }

        // Timer Bar
        if (roi.timer_bar) {
            boxes.push({ key: "timer_bar", label: "Timer Bar", cat: "actions", color: "#eab308", box: roi.timer_bar });
        }

        // Seats
        if (roi.seats && Array.isArray(roi.seats)) {
            roi.seats.forEach(seat => {
                const sNum = seat.seat_number;
                const pfx = `Seat ${sNum}`;
                const cat = "seats";
                const col = seat.is_hero ? "#00f2fe" : "#a855f7";

                if (seat.avatar) boxes.push({ key: `seat_${sNum}_avatar`, label: `${pfx}: Avatar`, cat, color: col, box: seat.avatar });
                if (seat.nameplate) boxes.push({ key: `seat_${sNum}_nameplate`, label: `${pfx}: Nameplate`, cat, color: col, box: seat.nameplate });
                if (seat.stack) boxes.push({ key: `seat_${sNum}_stack`, label: `${pfx}: Stack`, cat, color: col, box: seat.stack });
                if (seat.bet) boxes.push({ key: `seat_${sNum}_bet`, label: `${pfx}: Bet`, cat, color: "#eab308", box: seat.bet });
                if (seat.active_badge) boxes.push({ key: `seat_${sNum}_active`, label: `${pfx}: Active Badge`, cat, color: "#10b981", box: seat.active_badge });
                if (seat.cards) {
                    seat.cards.forEach((cb, ci) => {
                        boxes.push({ key: `seat_${sNum}_card_${ci}`, label: `${pfx}: Card ${ci + 1}`, cat, color: col, box: cb });
                    });
                }
            });
        }

        return boxes;
    }

    function getActiveBoxItem() {
        const boxes = getFlattenedBoxes(state.roi);
        return boxes.find(b => b.key === state.activeKey) || boxes[0];
    }

    // =========================================================================
    // Canvas Rendering Engine
    // =========================================================================
    function resizeCanvases() {
        const w = state.frameWidth;
        const h = state.frameHeight;

        elements.bgImageCanvas.width = w;
        elements.bgImageCanvas.height = h;
        elements.roiOverlayCanvas.width = w;
        elements.roiOverlayCanvas.height = h;

        elements.canvasStage.style.width = `${w * state.zoom}px`;
        elements.canvasStage.style.height = `${h * state.zoom}px`;

        elements.frameDimDisplay.textContent = `${w} x ${h} px`;
    }

    function drawBackground() {
        const ctx = elements.bgImageCanvas.getContext("2d");
        ctx.clearRect(0, 0, state.frameWidth, state.frameHeight);

        if (state.currentImage) {
            ctx.drawImage(state.currentImage, 0, 0, state.frameWidth, state.frameHeight);
            elements.emptyCanvasState.classList.add("hidden");
        } else {
            elements.emptyCanvasState.classList.remove("hidden");
        }
    }

    function drawOverlay() {
        const ctx = elements.roiOverlayCanvas.getContext("2d");
        const w = state.frameWidth;
        const h = state.frameHeight;

        ctx.clearRect(0, 0, w, h);

        const boxes = getFlattenedBoxes(state.roi);

        boxes.forEach(item => {
            if (state.filter !== "all" && item.cat !== state.filter) {
                return;
            }

            const isSelected = item.key === state.activeKey;
            const b = item.box;
            if (!b) return;

            const px = b.x * w;
            const py = b.y * h;
            const pw = b.width * w;
            const ph = b.height * h;

            // Box Fill
            ctx.fillStyle = isSelected ? hexToRgba(item.color, 0.28) : hexToRgba(item.color, 0.08);
            ctx.fillRect(px, py, pw, ph);

            // Box Border
            ctx.strokeStyle = item.color;
            ctx.lineWidth = isSelected ? 2 : 1;
            if (isSelected) {
                ctx.setLineDash([]);
                ctx.shadowColor = item.color;
                ctx.shadowBlur = 8;
            } else {
                ctx.setLineDash([3, 2]);
                ctx.shadowBlur = 0;
            }
            ctx.strokeRect(px, py, pw, ph);
            ctx.shadowBlur = 0;
            ctx.setLineDash([]);

            // Label tag
            if (isSelected || pw > 35) {
                ctx.font = "bold 10px monospace";
                const labelText = item.label;
                const textWidth = ctx.measureText(labelText).width;
                
                ctx.fillStyle = "rgba(10, 14, 22, 0.85)";
                ctx.fillRect(px, Math.max(0, py - 14), textWidth + 8, 14);
                
                ctx.fillStyle = isSelected ? "#00f2fe" : item.color;
                ctx.fillText(labelText, px + 4, Math.max(10, py - 3));
            }

            // Draw 4 Corner Handles if Selected
            if (isSelected) {
                drawResizeHandle(ctx, px, py);             // NW
                drawResizeHandle(ctx, px + pw, py);        // NE
                drawResizeHandle(ctx, px + pw, py + ph);   // SE
                drawResizeHandle(ctx, px, py + ph);        // SW
            }
        });
    }

    function drawResizeHandle(ctx, x, y) {
        const size = 6;
        ctx.fillStyle = "#ffffff";
        ctx.strokeStyle = "#00f2fe";
        ctx.lineWidth = 1.5;
        ctx.fillRect(x - size / 2, y - size / 2, size, size);
        ctx.strokeRect(x - size / 2, y - size / 2, size, size);
    }

    function hexToRgba(hex, alpha) {
        let c = hex.replace("#", "");
        if (c.length === 3) c = c.split("").map(x => x + x).join("");
        const num = parseInt(c, 16);
        return `rgba(${(num >> 16) & 255}, ${(num >> 8) & 255}, ${num & 255}, ${alpha})`;
    }

    // =========================================================================
    // Interactive Canvas Mouse Handling
    // =========================================================================
    function getCanvasRelativeCoords(e) {
        const rect = elements.roiOverlayCanvas.getBoundingClientRect();
        const clientX = e.clientX;
        const clientY = e.clientY;
        const scaleX = state.frameWidth / rect.width;
        const scaleY = state.frameHeight / rect.height;

        const xPx = (clientX - rect.left) * scaleX;
        const yPx = (clientY - rect.top) * scaleY;

        return {
            xPx,
            yPx,
            relX: Math.max(0, Math.min(1, xPx / state.frameWidth)),
            relY: Math.max(0, Math.min(1, yPx / state.frameHeight))
        };
    }

    function findHandleAt(item, xPx, yPx) {
        if (!item || !item.box) return null;
        const b = item.box;
        const w = state.frameWidth;
        const h = state.frameHeight;
        const px = b.x * w;
        const py = b.y * h;
        const pw = b.width * w;
        const ph = b.height * h;
        const threshold = 8;

        if (Math.hypot(xPx - px, yPx - py) <= threshold) return "nw";
        if (Math.hypot(xPx - (px + pw), yPx - py) <= threshold) return "ne";
        if (Math.hypot(xPx - (px + pw), yPx - (py + ph)) <= threshold) return "se";
        if (Math.hypot(xPx - px, yPx - (py + ph)) <= threshold) return "sw";

        return null;
    }

    function findBoxAt(relX, relY) {
        const boxes = getFlattenedBoxes(state.roi);
        // Prioritize active selection
        const active = boxes.find(b => b.key === state.activeKey);
        if (active && isInsideBox(active.box, relX, relY)) {
            return active;
        }

        for (let i = boxes.length - 1; i >= 0; i--) {
            const item = boxes[i];
            if (state.filter !== "all" && item.cat !== state.filter) continue;
            if (isInsideBox(item.box, relX, relY)) {
                return item;
            }
        }
        return null;
    }

    function isInsideBox(box, relX, relY) {
        if (!box) return false;
        return relX >= box.x && relX <= box.x + box.width &&
               relY >= box.y && relY <= box.y + box.height;
    }

    function onCanvasMouseMove(e) {
        const { xPx, yPx, relX, relY } = getCanvasRelativeCoords(e);
        elements.cursorPosDisplay.textContent = `X: ${relX.toFixed(3)}, Y: ${relY.toFixed(3)}`;

        // Dragging / Resizing
        if (state.dragState) {
            const ds = state.dragState;
            const dx = relX - ds.startX;
            const dy = relY - ds.startY;
            const b = ds.box;

            if (ds.type === "move") {
                b.x = Math.max(0, Math.min(1 - b.width, ds.orig.x + dx));
                b.y = Math.max(0, Math.min(1 - b.height, ds.orig.y + dy));
            } else if (ds.type === "se") {
                b.width = Math.max(0.005, Math.min(1 - b.x, ds.orig.width + dx));
                b.height = Math.max(0.005, Math.min(1 - b.y, ds.orig.height + dy));
            } else if (ds.type === "nw") {
                const newX = Math.max(0, Math.min(ds.orig.x + ds.orig.width - 0.005, ds.orig.x + dx));
                const newY = Math.max(0, Math.min(ds.orig.y + ds.orig.height - 0.005, ds.orig.y + dy));
                b.width = ds.orig.width + (ds.orig.x - newX);
                b.height = ds.orig.height + (ds.orig.y - newY);
                b.x = newX;
                b.y = newY;
            } else if (ds.type === "ne") {
                const newY = Math.max(0, Math.min(ds.orig.y + ds.orig.height - 0.005, ds.orig.y + dy));
                b.width = Math.max(0.005, Math.min(1 - b.x, ds.orig.width + dx));
                b.height = ds.orig.height + (ds.orig.y - newY);
                b.y = newY;
            } else if (ds.type === "sw") {
                const newX = Math.max(0, Math.min(ds.orig.x + ds.orig.width - 0.005, ds.orig.x + dx));
                b.width = ds.orig.width + (ds.orig.x - newX);
                b.height = Math.max(0.005, Math.min(1 - b.y, ds.orig.height + dy));
                b.x = newX;
            }

            syncPropertiesToInputs();
            drawOverlay();
            return;
        }

        // Hover Detection
        const activeItem = getActiveBoxItem();
        const handle = findHandleAt(activeItem, xPx, yPx);
        if (handle) {
            elements.roiOverlayCanvas.style.cursor = `${handle}-resize`;
            return;
        }

        const box = findBoxAt(relX, relY);
        if (box) {
            elements.roiOverlayCanvas.style.cursor = "move";
        } else {
            elements.roiOverlayCanvas.style.cursor = "crosshair";
        }
    }

    function onCanvasMouseDown(e) {
        const { xPx, yPx, relX, relY } = getCanvasRelativeCoords(e);
        const activeItem = getActiveBoxItem();

        // Check if clicked a handle on the active box
        const handle = findHandleAt(activeItem, xPx, yPx);
        if (handle && activeItem.box) {
            state.dragState = {
                type: handle,
                startX: relX,
                startY: relY,
                box: activeItem.box,
                orig: { ...activeItem.box }
            };
            return;
        }

        // Check if clicked any box
        const box = findBoxAt(relX, relY);
        if (box) {
            state.activeKey = box.key;
            elements.regionSelect.value = box.key;
            syncPropertiesToInputs();

            state.dragState = {
                type: "move",
                startX: relX,
                startY: relY,
                box: box.box,
                orig: { ...box.box }
            };
            drawOverlay();
        }
    }

    function onCanvasMouseUp() {
        if (state.dragState) {
            state.dragState = null;
            updateJSONPreview();
        }
    }

    // =========================================================================
    // Properties & Form Inputs Synchronization
    // =========================================================================
    function syncPropertiesToInputs() {
        const item = getActiveBoxItem();
        if (!item || !item.box) return;

        elements.selectedBoxName.textContent = `${item.label} [${item.key}]`;
        elements.inputX.value = item.box.x.toFixed(3);
        elements.inputY.value = item.box.y.toFixed(3);
        elements.inputWidth.value = item.box.width.toFixed(3);
        elements.inputHeight.value = item.box.height.toFixed(3);

        updateJSONPreview();
    }

    function onCoordinateInputChange() {
        const item = getActiveBoxItem();
        if (!item || !item.box) return;

        item.box.x = parseFloat(elements.inputX.value) || 0;
        item.box.y = parseFloat(elements.inputY.value) || 0;
        item.box.width = parseFloat(elements.inputWidth.value) || 0.01;
        item.box.height = parseFloat(elements.inputHeight.value) || 0.01;

        drawOverlay();
        updateJSONPreview();
    }

    function nudgeBox(dx, dy) {
        const item = getActiveBoxItem();
        if (!item || !item.box) return;

        item.box.x = Math.max(0, Math.min(1 - item.box.width, +(item.box.x + dx).toFixed(3)));
        item.box.y = Math.max(0, Math.min(1 - item.box.height, +(item.box.y + dy).toFixed(3)));

        syncPropertiesToInputs();
        drawOverlay();
    }

    function updateJSONPreview() {
        elements.roiJsonTextarea.value = JSON.stringify(state.roi, null, 2);
    }

    // =========================================================================
    // Server Synchronization & API Requests
    // =========================================================================
    async function loadFromServer() {
        try {
            const res = await fetch("/api/v1/roi");
            if (res.ok) {
                const data = await res.json();
                state.roi = data;
                syncPropertiesToInputs();
                drawOverlay();
                showToast("Loaded ROI configuration from server");
            } else {
                showToast("Failed to load ROI from server", true);
            }
        } catch (err) {
            console.error("loadFromServer error:", err);
            showToast("Server unreachable", true);
        }
    }

    async function saveToServer() {
        try {
            const res = await fetch("/api/v1/roi", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(state.roi)
            });
            if (res.ok) {
                showToast("Saved ROI configuration to server!");
            } else {
                showToast("Failed to save ROI to server", true);
            }
        } catch (err) {
            console.error("saveToServer error:", err);
            showToast("Server error during save", true);
        }
    }

    async function fetchSnapshot() {
        try {
            const res = await fetch(`/api/v1/snapshot?t=${Date.now()}`);
            if (res.ok) {
                const blob = await res.blob();
                const img = new Image();
                img.onload = function () {
                    state.currentImage = img;
                    state.frameWidth = img.naturalWidth || img.width;
                    state.frameHeight = img.naturalHeight || img.height;
                    resizeCanvases();
                    drawBackground();
                    drawOverlay();
                };
                img.src = URL.createObjectURL(blob);
            }
        } catch (err) {
            // No snapshot available yet
        }
    }

    async function fetchWindows() {
        try {
            const res = await fetch("/api/v1/windows");
            if (res.ok) {
                const windows = await res.json();
                elements.windowSelect.innerHTML = "";
                if (!windows || windows.length === 0) {
                    elements.windowSelect.innerHTML = '<option value="">No windows detected</option>';
                    return;
                }

                windows.forEach(w => {
                    const opt = document.createElement("option");
                    opt.value = w.id;
                    opt.textContent = `${w.title || "Untitled"} (${w.owner_name || "App"})`;
                    elements.windowSelect.appendChild(opt);
                });
            }
        } catch (err) {
            elements.windowSelect.innerHTML = '<option value="">Window API unavailable</option>';
        }
    }

    // =========================================================================
    // Synthetic Canvas Generator & File Upload
    // =========================================================================
    function generateSyntheticPokerTable() {
        const offscreen = document.createElement("canvas");
        offscreen.width = 1000;
        offscreen.height = 800;
        const ctx = offscreen.getContext("2d");

        // Green Felt Oval
        ctx.fillStyle = "#0a130e";
        ctx.fillRect(0, 0, 1000, 800);

        ctx.fillStyle = "#0f3824";
        ctx.beginPath();
        ctx.ellipse(500, 400, 440, 320, 0, 0, Math.PI * 2);
        ctx.fill();

        ctx.strokeStyle = "#1a5c3a";
        ctx.lineWidth = 12;
        ctx.stroke();

        // Watermark
        ctx.fillStyle = "rgba(255, 255, 255, 0.05)";
        ctx.font = "bold 28px sans-serif";
        ctx.textAlign = "center";
        ctx.fillText("COINPOKER 6-MAX SYNTHETIC TABLE", 500, 360);

        // Community Board Placeholder
        ctx.fillStyle = "rgba(0, 0, 0, 0.35)";
        ctx.fillRect(280, 320, 440, 100);

        const img = new Image();
        img.onload = function () {
            state.currentImage = img;
            state.frameWidth = 1000;
            state.frameHeight = 800;
            resizeCanvases();
            drawBackground();
            drawOverlay();
        };
        img.src = offscreen.toDataURL();
    }

    function showToast(msg, isError = false) {
        elements.toastBanner.textContent = msg;
        elements.toastBanner.style.background = isError ? "#991b1b" : "#065f46";
        elements.toastBanner.style.borderColor = isError ? "#ef4444" : "#10b981";
        elements.toastBanner.classList.add("show");
        setTimeout(() => {
            elements.toastBanner.classList.remove("show");
        }, 3000);
    }

    // =========================================================================
    // Event Binding
    // =========================================================================
    function setupEventListeners() {
        // Canvas Mouse Events
        elements.roiOverlayCanvas.addEventListener("mousemove", onCanvasMouseMove);
        elements.roiOverlayCanvas.addEventListener("mousedown", onCanvasMouseDown);
        window.addEventListener("mouseup", onCanvasMouseUp);

        // Filter Chips
        document.querySelectorAll(".filter-chip").forEach(chip => {
            chip.addEventListener("click", () => {
                document.querySelectorAll(".filter-chip").forEach(c => c.classList.remove("active"));
                chip.classList.add("active");
                state.filter = chip.dataset.filter;
                drawOverlay();
            });
        });

        // Region Select
        elements.regionSelect.addEventListener("change", (e) => {
            state.activeKey = e.target.value;
            syncPropertiesToInputs();
            drawOverlay();
        });

        // Numeric Coordinates
        [elements.inputX, elements.inputY, elements.inputWidth, elements.inputHeight].forEach(inp => {
            inp.addEventListener("input", onCoordinateInputChange);
        });

        // Nudge Buttons
        elements.nudgeUp.addEventListener("click", () => nudgeBox(0, -0.002));
        elements.nudgeDown.addEventListener("click", () => nudgeBox(0, 0.002));
        elements.nudgeLeft.addEventListener("click", () => nudgeBox(-0.002, 0));
        elements.nudgeRight.addEventListener("click", () => nudgeBox(0.002, 0));

        // Zoom Controls
        elements.btnZoomIn.addEventListener("click", () => {
            state.zoom = Math.min(2.5, +(state.zoom + 0.15).toFixed(2));
            elements.zoomLevelDisplay.textContent = `${Math.round(state.zoom * 100)}%`;
            resizeCanvases();
            drawBackground();
            drawOverlay();
        });
        elements.btnZoomOut.addEventListener("click", () => {
            state.zoom = Math.max(0.4, +(state.zoom - 0.15).toFixed(2));
            elements.zoomLevelDisplay.textContent = `${Math.round(state.zoom * 100)}%`;
            resizeCanvases();
            drawBackground();
            drawOverlay();
        });
        elements.btnZoomReset.addEventListener("click", () => {
            state.zoom = 1.0;
            elements.zoomLevelDisplay.textContent = "100%";
            resizeCanvases();
            drawBackground();
            drawOverlay();
        });

        // Server Sync Buttons
        elements.btnLoadROI.addEventListener("click", loadFromServer);
        elements.btnSaveROI.addEventListener("click", saveToServer);
        elements.btnResetDefaults.addEventListener("click", () => {
            if (confirm("Reset to default CoinPoker 6-Max ROI layout?")) {
                state.roi = getDefaultROIConfig();
                syncPropertiesToInputs();
                drawOverlay();
                showToast("Reset to CoinPoker 6-Max defaults");
            }
        });

        // Snapshot & Live Stream
        elements.btnFetchSnapshot.addEventListener("click", fetchSnapshot);
        elements.btnRefreshWindows.addEventListener("click", fetchWindows);

        elements.btnToggleLiveStream.addEventListener("click", () => {
            state.liveStream = !state.liveStream;
            elements.streamStatusText.textContent = state.liveStream ? "ON" : "OFF";
            const dot = elements.btnToggleLiveStream.querySelector(".stream-dot");
            if (state.liveStream) {
                dot.className = "stream-dot dot-active";
                startStreamLoop();
            } else {
                dot.className = "stream-dot dot-paused";
                clearInterval(state.streamInterval);
            }
        });

        // Upload Screenshot
        elements.fileUploadInput.addEventListener("change", (e) => {
            const file = e.target.files[0];
            if (file) {
                const img = new Image();
                img.onload = function () {
                    state.currentImage = img;
                    state.frameWidth = img.naturalWidth || img.width;
                    state.frameHeight = img.naturalHeight || img.height;
                    resizeCanvases();
                    drawBackground();
                    drawOverlay();
                    showToast("Uploaded custom image");
                };
                img.src = URL.createObjectURL(file);
            }
        });

        // Sample synthetic table button
        elements.btnLoadSampleImage.addEventListener("click", generateSyntheticPokerTable);

        // JSON Actions
        elements.btnCopyJSON.addEventListener("click", () => {
            navigator.clipboard.writeText(elements.roiJsonTextarea.value);
            showToast("Copied ROI JSON to clipboard");
        });

        elements.btnPasteJSON.addEventListener("click", async () => {
            try {
                const text = await navigator.clipboard.readText();
                const parsed = JSON.parse(text);
                state.roi = parsed;
                syncPropertiesToInputs();
                drawOverlay();
                showToast("Imported ROI JSON from clipboard");
            } catch (err) {
                showToast("Invalid JSON in clipboard", true);
            }
        });

        elements.btnDownloadJSON.addEventListener("click", () => {
            const dataStr = "data:text/json;charset=utf-8," + encodeURIComponent(elements.roiJsonTextarea.value);
            const downloadAnchor = document.createElement("a");
            downloadAnchor.setAttribute("href", dataStr);
            downloadAnchor.setAttribute("download", "poker_roi_config.json");
            document.body.appendChild(downloadAnchor);
            downloadAnchor.click();
            downloadAnchor.remove();
        });
    }

    function startStreamLoop() {
        clearInterval(state.streamInterval);
        state.streamInterval = setInterval(fetchSnapshot, 1000);
    }

    // =========================================================================
    // Initialization
    // =========================================================================
    function init() {
        setupEventListeners();
        resizeCanvases();
        syncPropertiesToInputs();
        loadFromServer();
        fetchWindows();
        fetchSnapshot();
        startStreamLoop();
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
