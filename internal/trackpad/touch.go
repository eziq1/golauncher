// Package trackpad watches the touches on Mac trackpads for a three-finger
// tap, which opens the Action Ring (docs/action-ring.md §10, R127). The
// touches come from the private MultitouchSupport framework (darwin only);
// decoding them and telling a tap from a swipe or a drag are plain Go, here
// and in tap.go, tested with synthetic frames.
package trackpad

import (
	"encoding/binary"
	"math"
)

// The layout of one touch as MultitouchSupport reports it to a contact
// frame callback (MTTouch, also called Finger). The framework is private;
// the layout is the one documented by the open-source projects that read
// it: Erling Ellingsen's 2008 write-up of the framework ("multitouch.c",
// struct Finger), MiddleClick's header and OpenMultitouchSupport's MTTouch
// (MIT), which agree on it. It is the same on amd64 and arm64: int and
// float are 4 bytes on both, double is 8 bytes aligned to 8 on both (hence
// the padding after frame), and both are little-endian.
//
//	offset size type    field
//	     0    4 int32   frame
//	     4    4         (padding: timestamp is aligned to 8)
//	     8    8 float64 timestamp, seconds
//	    16    4 int32   identifier: the same finger keeps it while it touches
//	    20    4 int32   state (touchState*)
//	    24    4 int32   finger id (which finger the framework thinks it is)
//	    28    4 int32   hand id
//	    32    4 float32 normalized position x, 0 (left) to 1 (right)
//	    36    4 float32 normalized position y, 0 (bottom) to 1 (top)
//	    40    4 float32 normalized velocity x
//	    44    4 float32 normalized velocity y
//	    48    4 float32 total (size of the contact; "zTotal")
//	    52    4 float32 pressure (often 0)
//	    56    4 float32 angle of the contact's ellipse
//	    60    4 float32 major axis
//	    64    4 float32 minor axis
//	    68   16 float32 ×4 absolute position and velocity, millimetres
//	    84    4 int32   unknown
//	    88    4 int32   unknown
//	    92    4 float32 density
//	    96              size (the stride of the array)
const (
	touchSize      = 96
	offIdentifier  = 16
	offState       = 20
	offNormalizedX = 32
	offNormalizedY = 36
)

// Touch states (MTTouchState in OpenMultitouchSupport's header): a finger
// comes in range (hovering), makes the touch, touches, breaks the touch and
// lingers before it is out of range.
const (
	touchStateNotTracking  = 0
	touchStateStartInRange = 1
	touchStateHoverInRange = 2
	touchStateMakeTouch    = 3
	touchStateTouching     = 4
	touchStateBreakTouch   = 5
	touchStateLinger       = 6
	touchStateOutOfRange   = 7
)

// maxTouches bounds the touches read from one frame: more than ten fingers
// are not a gesture, and a count that large is a misread one.
const maxTouches = 16

// Touch is one finger of a frame: its identifier, state and normalized
// position (0–1 across the trackpad).
type Touch struct {
	ID    int32
	State int32
	X, Y  float32
}

// Down reports whether the finger is on the surface: it made the touch or
// touches. Hovering, lifting and lingering fingers are not.
func (t Touch) Down() bool {
	return t.State == touchStateMakeTouch || t.State == touchStateTouching
}

// decodeTouches reads the touches laid out in b (touchSize bytes each, as
// the framework passes them) into dst, and returns how many it read: as
// many as fit in both.
func decodeTouches(dst []Touch, b []byte) int {
	n := min(len(dst), len(b)/touchSize)
	for i := range n {
		t := b[i*touchSize : (i+1)*touchSize]
		dst[i] = Touch{
			ID:    int32(binary.LittleEndian.Uint32(t[offIdentifier:])),
			State: int32(binary.LittleEndian.Uint32(t[offState:])),
			X:     math.Float32frombits(binary.LittleEndian.Uint32(t[offNormalizedX:])),
			Y:     math.Float32frombits(binary.LittleEndian.Uint32(t[offNormalizedY:])),
		}
	}
	return n
}
