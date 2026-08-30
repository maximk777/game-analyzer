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

	// Use AppleScript / JXA to query CoreGraphics window list
	script := `
	ObjC.import('Foundation');
	ObjC.import('CoreGraphics');
	var rawList = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly | $.kCGWindowListExcludeDesktopElements, $.kCGNullWindowID);
	var nsArray = ObjC.castRefToObject(rawList);
	var res = [];
	for (var i = 0; i < nsArray.count; i++) {
		var item = nsArray.objectAtIndex(i);
		var id = item.objectForKey('kCGWindowNumber');
		var owner = item.objectForKey('kCGWindowOwnerName');
		var title = item.objectForKey('kCGWindowName');
		var bounds = item.objectForKey('kCGWindowBounds');
		var x = (bounds && bounds.objectForKey('X')) ? bounds.objectForKey('X').js : 0;
		var y = (bounds && bounds.objectForKey('Y')) ? bounds.objectForKey('Y').js : 0;
		var w = (bounds && bounds.objectForKey('Width')) ? bounds.objectForKey('Width').js : 0;
		var h = (bounds && bounds.objectForKey('Height')) ? bounds.objectForKey('Height').js : 0;
		res.push({
			id: id ? id.js : 0,
			owner_name: owner ? owner.js : '',
			title: title ? title.js : '',
			x: x,
			y: y,
			w: w,
			h: h,
			is_on_screen: true
		});
	}
	JSON.stringify(res);
	`
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("osascript failed: %w, stderr: %s", err, stderr.String())
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
