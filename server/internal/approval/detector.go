package approval

import (
	"bytes"
	"regexp"
	"sync"
	"time"
)

const (
	defaultMaxTailBytes            = 1024
	maxNonMatchingLinesAfterMatch  = 2
)

// Detector inspects streaming PTY output for agent approval prompts and fires
// a callback once output settles for the configured idle duration.
type Detector struct {
	patterns     []*regexp.Regexp
	idleDuration time.Duration
	maxTailBytes int
	onApproval   func(pattern string)

	mu             sync.Mutex
	ansi           ansiStripper
	tail           []byte
	timer          *time.Timer
	gen            uint64
	notified       bool
	pendingPattern string
	closed         bool
}

// NewDetector creates a non-blocking approval prompt detector for a session.
func NewDetector(patterns []*regexp.Regexp, idleDuration time.Duration, onApproval func(pattern string)) *Detector {
	copied := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if p != nil {
			copied = append(copied, p)
		}
	}
	if idleDuration < 0 {
		idleDuration = 0
	}
	return &Detector{
		patterns:     copied,
		idleDuration: idleDuration,
		maxTailBytes: defaultMaxTailBytes,
		onApproval:   onApproval,
	}
}

// FeedOutput processes a raw PTY output chunk without blocking the caller.
func (d *Detector) FeedOutput(chunk []byte) {
	if len(chunk) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed || len(d.patterns) == 0 {
		return
	}

	stripped := d.ansi.strip(chunk)
	if len(stripped) == 0 {
		return
	}

	d.tail = append(d.tail, stripped...)
	if len(d.tail) > d.maxTailBytes {
		excess := len(d.tail) - d.maxTailBytes
		copy(d.tail, d.tail[excess:])
		d.tail = d.tail[:d.maxTailBytes]
	}

	bestPattern, bestEnd := d.findLatestMatch()
	if bestEnd >= 0 && nonEmptyLinesAfter(d.tail[bestEnd:]) <= maxNonMatchingLinesAfterMatch {
		if d.notified {
			return
		}
		d.stopTimerLocked()
		d.gen++
		myGen := d.gen
		d.pendingPattern = bestPattern
		d.timer = time.AfterFunc(d.idleDuration, func() {
			d.fire(myGen)
		})
		return
	}

	// Output continued with non-matching lines or no match remains in the tail window.
	d.stopTimerLocked()
	d.gen++
	d.notified = false
	d.pendingPattern = ""
	if bestEnd >= 0 {
		// Trim past the stale match so subsequent small chunks do not re-trigger it.
		remaining := len(d.tail) - bestEnd
		copy(d.tail, d.tail[bestEnd:])
		d.tail = d.tail[:remaining]
	}
}

// FeedInput cancels any pending approval timer and resets notification state after user input.
func (d *Detector) FeedInput() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}
	d.stopTimerLocked()
	d.gen++
	d.notified = false
	d.pendingPattern = ""
	d.tail = d.tail[:0]
}

// WaitingApproval reports whether an approval prompt notification is currently active.
func (d *Detector) WaitingApproval() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.closed && d.notified
}

// Close permanently stops the detector and cancels any pending timer.
func (d *Detector) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return
	}
	d.closed = true
	d.stopTimerLocked()
	d.gen++
	d.notified = false
	d.pendingPattern = ""
	d.onApproval = nil
}

func (d *Detector) stopTimerLocked() {
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func (d *Detector) findLatestMatch() (pattern string, endIdx int) {
	endIdx = -1
	for _, re := range d.patterns {
		locs := re.FindAllIndex(d.tail, -1)
		if len(locs) == 0 {
			continue
		}
		last := locs[len(locs)-1]
		if last[1] > endIdx {
			endIdx = last[1]
			pattern = re.String()
		}
	}
	return pattern, endIdx
}

func (d *Detector) fire(gen uint64) {
	d.mu.Lock()
	if d.closed || d.gen != gen || d.notified {
		d.mu.Unlock()
		return
	}
	d.notified = true
	d.timer = nil
	cb := d.onApproval
	pat := d.pendingPattern
	d.mu.Unlock()

	if cb != nil {
		cb(pat)
	}
}

func nonEmptyLinesAfter(after []byte) int {
	if len(after) == 0 {
		return 0
	}
	parts := bytes.Split(after, []byte{'\n'})
	if len(parts) <= 1 {
		return 0
	}
	count := 0
	for _, line := range parts[1:] {
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

type ansiState uint8

const (
	stateNormal ansiState = iota
	stateEsc
	stateEscInterm
	stateCSI
	stateOSC
	stateOSCEsc
)

// StripANSI removes ANSI CSI/OSC/ESC sequences and non-printable control bytes.
func StripANSI(in []byte) []byte {
	var s ansiStripper
	return s.strip(in)
}

// ansiStripper removes ANSI CSI/OSC/ESC sequences and non-printable control bytes
// across arbitrary chunk boundaries.
type ansiStripper struct {
	state ansiState
}

func (s *ansiStripper) strip(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for _, b := range in {
		switch s.state {
		case stateNormal:
			switch {
			case b == 0x1b:
				s.state = stateEsc
			case b == '\n' || b == '\t':
				out = append(out, b)
			case b < 0x20 || b == 0x7f:
				// Drop CR and other C0/DEL control bytes.
			default:
				out = append(out, b)
			}
		case stateEsc:
			switch {
			case b == '[':
				s.state = stateCSI
			case b == ']', b == 'P', b == 'X', b == '^', b == '_':
				s.state = stateOSC
			case b >= 0x20 && b <= 0x2f:
				s.state = stateEscInterm
			default:
				s.state = stateNormal
			}
		case stateEscInterm:
			if b < 0x20 || b > 0x2f {
				s.state = stateNormal
			}
		case stateCSI:
			switch {
			case b >= 0x40 && b <= 0x7e:
				s.state = stateNormal
			case b == 0x1b:
				s.state = stateEsc
			case b < 0x20 || b > 0x3f:
				s.state = stateNormal
			}
		case stateOSC:
			switch b {
			case 0x07:
				s.state = stateNormal
			case 0x1b:
				s.state = stateOSCEsc
			}
		case stateOSCEsc:
			switch b {
			case '\\':
				s.state = stateNormal
			case 0x1b:
				s.state = stateOSCEsc
			default:
				s.state = stateOSC
			}
		}
	}
	return out
}
