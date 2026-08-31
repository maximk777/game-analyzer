package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
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

// capture runs screencapture with the given selection flag and decodes the PNG.
//
// screencapture has no way to write to standard output. Its usage line is
// `screencapture [-options] [files]`, and a trailing "-" is taken as a file
// named "-", not as a stream. Both callers here passed "-" and read stdout, so
// every capture wrote a PNG into the process's working directory under that
// name and then failed to decode nothing at all -- a path that could not
// return a frame under any circumstances. The stray file reached the
// repository twice, and was deleted by hand once (commit 8fe85d2) before
// anybody looked at why it kept coming back.
//
// So the file is the interface: written where the operating system puts
// temporary files, read back, and removed.
func capture(sel string) (image.Image, error) {
	f, err := os.CreateTemp("", "poker-rta-frame-*.png")
	if err != nil {
		return nil, fmt.Errorf("creating a file for the frame: %w", err)
	}
	path := f.Name()
	// screencapture writes the file itself, and refuses to overwrite nothing;
	// the handle is only here to reserve a name nobody else will take.
	f.Close()
	defer os.Remove(path)

	cmd := exec.Command("screencapture", sel, "-x", "-o", "-tpng", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %w, stderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the captured frame: %w", err)
	}
	// An empty file is what screencapture leaves behind when it declines to
	// capture -- no screen-recording permission, or a window that has gone --
	// and it exits zero either way, so this is the only place that shows up.
	if len(data) == 0 {
		return nil, fmt.Errorf("screencapture wrote no frame (screen recording permission?)")
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode captured PNG: %w", err)
	}
	return img, nil
}

// CaptureWindow captures the contents of a window specified by WindowInfo.
func (g *NativeGrabber) CaptureWindow(win WindowInfo) (image.Image, error) {
	if win.ID == 0 {
		return nil, fmt.Errorf("invalid window id 0")
	}
	return capture("-l" + strconv.Itoa(int(win.ID)))
}

// CaptureRect captures a bounding rectangle on screen.
func (g *NativeGrabber) CaptureRect(rect image.Rectangle) (image.Image, error) {
	return capture(fmt.Sprintf("-R%d,%d,%d,%d", rect.Min.X, rect.Min.Y, rect.Dx(), rect.Dy()))
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
