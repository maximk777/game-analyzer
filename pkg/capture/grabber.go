package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os/exec"
	"strconv"
	"sync"
)

// FrameGrabber defines the interface for capturing screen regions or specific windows.
type FrameGrabber interface {
	CaptureWindow(win WindowInfo) (image.Image, error)
	CaptureRect(rect image.Rectangle) (image.Image, error)
}

// NativeGrabber captures window and screen regions on macOS using screencapture.
type NativeGrabber struct{}

// NewNativeGrabber creates a new macOS native frame grabber.
func NewNativeGrabber() *NativeGrabber {
	return &NativeGrabber{}
}

// CaptureWindow captures the contents of a window specified by WindowInfo without writing to disk.
func (g *NativeGrabber) CaptureWindow(win WindowInfo) (image.Image, error) {
	if win.ID == 0 {
		return nil, fmt.Errorf("invalid window id 0")
	}

	// screencapture -l<windowID> -x -o -tpng - (stream directly to stdout)
	cmd := exec.Command("screencapture", "-l"+strconv.Itoa(int(win.ID)), "-x", "-o", "-tpng", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %w, stderr: %s", err, stderr.String())
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to decode captured PNG: %w", err)
	}
	return img, nil
}

// CaptureRect captures a bounding rectangle on screen directly to memory.
func (g *NativeGrabber) CaptureRect(rect image.Rectangle) (image.Image, error) {
	rectStr := fmt.Sprintf("%d,%d,%d,%d", rect.Min.X, rect.Min.Y, rect.Dx(), rect.Dy())
	cmd := exec.Command("screencapture", "-R"+rectStr, "-x", "-o", "-tpng", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture rect failed: %w, stderr: %s", err, stderr.String())
	}

	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to decode captured PNG: %w", err)
	}
	return img, nil
}

// MockGrabber provides deterministic in-memory frames for tests and simulation.
type MockGrabber struct {
	mu    sync.RWMutex
	frame image.Image
}

// NewMockGrabber creates a new in-memory MockGrabber.
func NewMockGrabber(img image.Image) *MockGrabber {
	return &MockGrabber{frame: img}
}

// SetFrame updates the in-memory frame returned by mock capture methods.
func (m *MockGrabber) SetFrame(img image.Image) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frame = img
}

// CaptureWindow returns the mock frame or an error if no frame is set.
func (m *MockGrabber) CaptureWindow(win WindowInfo) (image.Image, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.frame == nil {
		return nil, fmt.Errorf("no mock frame set")
	}
	return m.frame, nil
}

// CaptureRect returns the mock frame or an error if no frame is set.
func (m *MockGrabber) CaptureRect(rect image.Rectangle) (image.Image, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.frame == nil {
		return nil, fmt.Errorf("no mock frame set")
	}
	return m.frame, nil
}
