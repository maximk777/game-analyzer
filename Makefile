SWIFT_SHARED := pkg/capture/table_vision.swift pkg/capture/card_templates.swift pkg/capture/rank_bitmap_templates.swift

.PHONY: all vision assets ranks server ui app test test-race clean

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

test:
	go test ./...

test-race:
	go test -race ./...

clean:
	rm -f bin/mac_vision_agent bin/parse_image bin/diag_recorder bin/snap bin/hud_panel
