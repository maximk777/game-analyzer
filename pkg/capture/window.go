package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os/exec"
	"runtime"
	"strings"
)

// WindowInfo represents an OS window and its bounding dimensions.
type WindowInfo struct {
	ID         uint32          `json:"id"`
	Title      string          `json:"title"`
	OwnerName  string          `json:"owner_name"`
	Bounds     image.Rectangle `json:"bounds"`
	IsOnScreen bool            `json:"is_on_screen"`
}

// FilterPokerWindow searches a list of windows for the best matching poker table.
func FilterPokerWindow(windows []WindowInfo, query string) *WindowInfo {
	query = strings.ToLower(query)

	// 1. Prioritize active table windows (containing table, hold'em, nlh, pot, etc.)
	for _, w := range windows {
		if !w.IsOnScreen {
			continue
		}
		title := strings.ToLower(w.Title)
		owner := strings.ToLower(w.OwnerName)

		if strings.Contains(owner, query) || strings.Contains(title, query) {
			if strings.Contains(title, "table") || strings.Contains(title, "hold'em") || strings.Contains(title, "nlh") || strings.Contains(title, "pot") {
				target := w
				return &target
			}
		}
	}

	// 2. Fallback to any active window matching owner or title
	for _, w := range windows {
		if !w.IsOnScreen {
			continue
		}
		title := strings.ToLower(w.Title)
		owner := strings.ToLower(w.OwnerName)

		if strings.Contains(owner, query) || strings.Contains(title, query) {
			target := w
			return &target
		}
	}

	return nil
}

// ListAllWindows enumerates on-screen windows on macOS.
func ListAllWindows() ([]WindowInfo, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("native window enumeration only implemented for macOS")
	}

	swiftScript := `import Cocoa
let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
var res: [[String: Any]] = []
for w in list {
    let id = w[kCGWindowNumber as String] as? Int ?? 0
    let owner = w[kCGWindowOwnerName as String] as? String ?? ""
    let title = w[kCGWindowName as String] as? String ?? ""
    let bounds = w[kCGWindowBounds as String] as? [String: Any] ?? [:]
    let x = bounds["X"] as? Double ?? 0
    let y = bounds["Y"] as? Double ?? 0
    let w = bounds["Width"] as? Double ?? 0
    let h = bounds["Height"] as? Double ?? 0
    res.append(["id": id, "owner_name": owner, "title": title, "x": x, "y": y, "w": w, "h": h, "is_on_screen": true])
}
let data = try! JSONSerialization.data(withJSONObject: res, options: [])
print(String(data: data, encoding: .utf8)!)`

	cmd := exec.Command("swift", "-e", swiftScript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("swift window enumeration failed: %w, stderr: %s", err, stderr.String())
	}

	var rawList []struct {
		ID         uint32  `json:"id"`
		OwnerName  string  `json:"owner_name"`
		Title      string  `json:"title"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
		W          float64 `json:"w"`
		H          float64 `json:"h"`
		IsOnScreen bool    `json:"is_on_screen"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rawList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal window list: %w", err)
	}

	res := make([]WindowInfo, len(rawList))
	for i, r := range rawList {
		res[i] = WindowInfo{
			ID:         r.ID,
			Title:      r.Title,
			OwnerName:  r.OwnerName,
			Bounds:     image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)),
			IsOnScreen: r.IsOnScreen,
		}
	}
	return res, nil
}
