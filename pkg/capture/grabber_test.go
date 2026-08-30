package capture

import (
	"image"
	"image/color"
	"testing"
)

func TestMockGrabber_Capture(t *testing.T) {
	testImg := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			testImg.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}

	mock := NewMockGrabber(testImg)

	// 1. CaptureWindow
	img, err := mock.CaptureWindow(WindowInfo{ID: 101})
	if err != nil {
		t.Fatalf("CaptureWindow failed: %v", err)
	}
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() != 600 {
		t.Errorf("unexpected image dimensions: %v", img.Bounds())
	}

	// 2. CaptureRect
	rectImg, err := mock.CaptureRect(image.Rect(10, 10, 100, 100))
	if err != nil {
		t.Fatalf("CaptureRect failed: %v", err)
	}
	if rectImg == nil {
		t.Fatal("expected non-nil image from CaptureRect")
	}

	// 3. SetFrame
	newImg := image.NewRGBA(image.Rect(0, 0, 400, 300))
	mock.SetFrame(newImg)
	img2, err := mock.CaptureWindow(WindowInfo{ID: 101})
	if err != nil {
		t.Fatalf("CaptureWindow failed after SetFrame: %v", err)
	}
	if img2.Bounds().Dx() != 400 || img2.Bounds().Dy() != 300 {
		t.Errorf("expected updated dimensions (400x300), got: %v", img2.Bounds())
	}

	// 4. Nil frame error
	mock.SetFrame(nil)
	_, err = mock.CaptureWindow(WindowInfo{ID: 101})
	if err == nil {
		t.Fatal("expected error when mock frame is nil")
	}
	_, err = mock.CaptureRect(image.Rect(0, 0, 100, 100))
	if err == nil {
		t.Fatal("expected error when mock frame is nil in CaptureRect")
	}
}

func TestNativeGrabber_Validation(t *testing.T) {
	grabber := NewNativeGrabber()
	if grabber == nil {
		t.Fatal("expected non-nil NativeGrabber")
	}

	// Invalid window ID 0
	_, err := grabber.CaptureWindow(WindowInfo{ID: 0})
	if err == nil {
		t.Fatal("expected error for window ID 0")
	}
}
