SWIFT_SHARED := pkg/capture/table_vision.swift pkg/capture/card_templates.swift pkg/capture/rank_bitmap_templates.swift

.PHONY: all vision assets ranks server ui app harness bench-guard test test-race clean

all: assets vision

# Card references, read out of the installed client. Optional: without them the
# recogniser falls back to text recognition. Nothing is redistributed -- the
# assets land under bin/, which is not in the repository.
assets:
	@python3 tools/extract_client_assets.py
	@$(MAKE) --no-print-directory ranks

# Rank glyphs as the client draws them, 64x64 greyscale, one per rank.
#
# They are not in this repository -- they are the client's artwork -- so they
# are copied in from wherever they were built. Absent, recognition falls back to
# references rendered from the client's font, which works and is a shade worse.
#
#   make ranks RANK_TEMPLATES=/path/to/rank_templates
RANK_TEMPLATES ?= $(HOME)/Desktop/coinpoker-tracker-min/rank_templates

ranks:
	@if [ -d "$(RANK_TEMPLATES)" ]; then \
		mkdir -p bin/assets/coinpoker/ranks; \
		cp "$(RANK_TEMPLATES)"/*.png bin/assets/coinpoker/ranks/ 2>/dev/null || true; \
		echo "rank glyphs: $$(ls bin/assets/coinpoker/ranks/*.png 2>/dev/null | wc -l | tr -d ' ') -> bin/assets/coinpoker/ranks"; \
	else \
		echo "rank glyphs: none at $(RANK_TEMPLATES); falling back to font-rendered references"; \
	fi

# The Swift binaries each compile the shared table analyser alongside their own
# entry point, so table_vision.swift stays the single source of card
# recognition for the live agent, the offline harness and the recorder.
vision: bin/mac_vision_agent bin/parse_image bin/diag_recorder bin/snap bin/hud_panel

bin/mac_vision_agent: $(SWIFT_SHARED) pkg/capture/mac_vision_agent.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

bin/parse_image: $(SWIFT_SHARED) pkg/capture/parse_image_tool.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

bin/diag_recorder: $(SWIFT_SHARED) pkg/capture/diag_recorder.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

# Captures the table window to a PNG. Diagnosis starts from a frame, so the
# tool that produces one is built alongside the tools that read one.
bin/snap: $(SWIFT_SHARED) pkg/capture/snap_tool.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

# The HUD as a floating panel pinned to the table window. A Chrome window
# cannot be kept above another application's window, which is when the HUD is
# actually read.
bin/hud_panel: $(SWIFT_SHARED) pkg/capture/hud_panel.swift
	@mkdir -p bin
	swiftc -parse-as-library $^ -o $@

# Running the thing, in two halves.
#
# They are separate on purpose. The server is what reads the table and decides;
# the interface is a window that shows what it decided. Starting them together
# meant every build opened a window, and a browser window at that -- one that
# cannot be kept above the poker client, which is the only place it is any use.
#
#   make server   the reader, the decisions and the HTTP/WebSocket endpoint
#   make ui       the floating panel, pinned to the table window
#   make app      both, server first
PORT ?= 8080

server: vision
	go run ./cmd/agent --port $(PORT)

ui: bin/hud_panel
	bin/hud_panel --url http://localhost:$(PORT)/hud.html

app: vision
	go run ./cmd/agent --port $(PORT) --open-hud

# The harness: plays the advisor out over whole hands against simulated
# opponents and reports what following it would have won.
#
#   make harness                     the standard run, against the population
#   make harness FIELD=pro,pro,pro,pro,pro    against a table of regulars
#   make harness HANDS=5000 LINEUPS=64        a longer, tighter measurement
#
# The first candidate is the baseline every other is compared against, hand by
# hand on identical decks. See docs/HARNESS.md.
HANDS ?= 2500
LINEUPS ?= 32
SEED ?= 1
FIELD ?=
CANDIDATES ?= pro,tool:stats,novice:stats@0.9

# Built, not `go run`. A long run and an edit to the source overlap sooner or
# later, and `go run` compiles at the moment it starts: two runs launched from
# one command line then measure two different programs. That has already
# happened once and the numbers looked plausible.
bin/harness: $(shell find pkg cmd -name '*.go' 2>/dev/null)
	@mkdir -p bin
	go build -o $@ ./cmd/harness

harness: bin/harness
	bin/harness -hands $(HANDS) -lineups $(LINEUPS) -seed $(SEED) \
		-candidates $(CANDIDATES) $(if $(FIELD),-field $(FIELD),) \
		-stack-min 100 -stack-max 100

# The same run, with a gate. Exits non-zero if GUARD has fallen more than two
# combined standard errors below its last recorded run of the same shape.
#
# Every run appends a line to bench/results.jsonl -- the commit, whether the
# tree was dirty, the configuration and the numbers -- so "did what I just
# change move it" is answered against a record instead of by measuring the
# baseline all over again. The ledger is committed on purpose: a measurement
# nobody kept is a measurement that has to be taken twice.
#
#   make bench-guard GUARD=tool:stats
GUARD ?= tool:stats

bench-guard: bin/harness
	bin/harness -hands $(HANDS) -lineups $(LINEUPS) -seed $(SEED) \
		-candidates $(CANDIDATES) $(if $(FIELD),-field $(FIELD),) \
		-stack-min 100 -stack-max 100 -guard $(GUARD)

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f bin/mac_vision_agent bin/parse_image bin/diag_recorder bin/snap bin/hud_panel bin/harness
