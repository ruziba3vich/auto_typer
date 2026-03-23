package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unsafe"
)

const defaultTypoRate = 0.07

// ---- uinput constants ----

const (
	uiSetEvbit  = 0x40045564
	uiSetKeybit = 0x40045565
	uiDevCreate = 0x5501
	uiDevDestroy = 0x5502

	evKey = 0x01
	evSyn = 0x00

	synReport = 0

	keyBackspace  = 14
	keyTab        = 15
	keyEnter      = 28
	keyLeftShift  = 42
	keySpace      = 57
)

// inputEvent matches the Linux struct input_event on amd64.
type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

// uinputUserDev matches struct uinput_user_dev (simplified for keyboard).
type uinputUserDev struct {
	Name      [80]byte
	ID        [8]byte  // input_id: bustype, vendor, product, version (4x uint16)
	FFEffects uint32
	Absmax    [64]int32
	Absmin    [64]int32
	Absfuzz   [64]int32
	Absflat   [64]int32
}

// Keyboard is a virtual keyboard backed by /dev/uinput.
type Keyboard struct {
	f *os.File
}

func ioctl(fd uintptr, cmd, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, cmd, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

func NewKeyboard() (*Keyboard, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w", err)
	}
	fd := f.Fd()

	// Enable EV_KEY and EV_SYN.
	if err := ioctl(fd, uiSetEvbit, evKey); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_EVBIT EV_KEY: %w", err)
	}

	// Enable all key codes we might need (0-255 covers full keyboard).
	for i := uintptr(1); i <= 255; i++ {
		ioctl(fd, uiSetKeybit, i)
	}

	// Write device info.
	var dev uinputUserDev
	copy(dev.Name[:], "auto_typer virtual keyboard")
	// BUS_USB=3, arbitrary vendor/product
	binary.LittleEndian.PutUint16(dev.ID[0:2], 3)    // bustype
	binary.LittleEndian.PutUint16(dev.ID[2:4], 0x1234) // vendor
	binary.LittleEndian.PutUint16(dev.ID[4:6], 0x5678) // product
	binary.LittleEndian.PutUint16(dev.ID[6:8], 1)      // version

	devBytes := (*[unsafe.Sizeof(dev)]byte)(unsafe.Pointer(&dev))[:]
	if _, err := f.Write(devBytes); err != nil {
		f.Close()
		return nil, fmt.Errorf("write uinput_user_dev: %w", err)
	}

	// Create the device.
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", err)
	}

	// Give the kernel a moment to register the device.
	time.Sleep(200 * time.Millisecond)

	return &Keyboard{f: f}, nil
}

func (kb *Keyboard) Close() {
	ioctl(kb.f.Fd(), uiDevDestroy, 0)
	kb.f.Close()
}

func (kb *Keyboard) emit(typ uint16, code uint16, value int32) error {
	ev := inputEvent{
		Type:  typ,
		Code:  code,
		Value: value,
	}
	buf := (*[unsafe.Sizeof(ev)]byte)(unsafe.Pointer(&ev))[:]
	_, err := kb.f.Write(buf)
	return err
}

func (kb *Keyboard) sync() error {
	return kb.emit(evSyn, synReport, 0)
}

func (kb *Keyboard) tap(code uint16) error {
	// Press, release, sync — all in one batch.
	if err := kb.emit(evKey, code, 1); err != nil {
		return err
	}
	if err := kb.emit(evKey, code, 0); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	// Small settle time so the kernel processes the event.
	time.Sleep(2 * time.Millisecond)
	return nil
}

func (kb *Keyboard) shiftTap(code uint16) error {
	// Shift down.
	if err := kb.emit(evKey, keyLeftShift, 1); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)

	// Key press + release.
	if err := kb.emit(evKey, code, 1); err != nil {
		return err
	}
	if err := kb.emit(evKey, code, 0); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)

	// Shift up.
	if err := kb.emit(evKey, keyLeftShift, 0); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	// Extra settle time to prevent shift bleeding into next char.
	time.Sleep(5 * time.Millisecond)
	return nil
}

// ---- character to keycode mapping ----

var keycodes = map[rune]uint16{
	'1': 2, '2': 3, '3': 4, '4': 5, '5': 6,
	'6': 7, '7': 8, '8': 9, '9': 10, '0': 11,
	'-': 12, '=': 13,
	'q': 16, 'w': 17, 'e': 18, 'r': 19, 't': 20,
	'y': 21, 'u': 22, 'i': 23, 'o': 24, 'p': 25,
	'[': 26, ']': 27,
	'a': 30, 's': 31, 'd': 32, 'f': 33, 'g': 34,
	'h': 35, 'j': 36, 'k': 37, 'l': 38, ';': 39,
	'\'': 40, '`': 41,
	'\\': 43,
	'z': 44, 'x': 45, 'c': 46, 'v': 47, 'b': 48,
	'n': 49, 'm': 50, ',': 51, '.': 52, '/': 53,
	' ': keySpace, '\t': keyTab, '\n': keyEnter,
}

var shiftedKeys = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5',
	'^': '6', '&': '7', '*': '8', '(': '9', ')': '0',
	'_': '-', '+': '=',
	'{': '[', '}': ']', '|': '\\',
	':': ';', '"': '\'', '~': '`',
	'<': ',', '>': '.', '?': '/',
}

func (kb *Keyboard) sendChar(r rune) error {
	// Uppercase letter.
	if unicode.IsUpper(r) {
		if code, ok := keycodes[unicode.ToLower(r)]; ok {
			return kb.shiftTap(code)
		}
	}
	// Direct keycode.
	if code, ok := keycodes[r]; ok {
		return kb.tap(code)
	}
	// Shifted symbol.
	if base, ok := shiftedKeys[r]; ok {
		if code, ok := keycodes[base]; ok {
			return kb.shiftTap(code)
		}
	}
	// Skip unmapped characters.
	return nil
}

func (kb *Keyboard) sendBackspace() error {
	return kb.tap(keyBackspace)
}

// ---- QWERTY neighbor map for typos ----

var qwertyNeighbors = map[rune][]rune{
	'q': {'w', 'a'},
	'w': {'q', 'e', 'a', 's'},
	'e': {'w', 'r', 's', 'd'},
	'r': {'e', 't', 'd', 'f'},
	't': {'r', 'y', 'f', 'g'},
	'y': {'t', 'u', 'g', 'h'},
	'u': {'y', 'i', 'h', 'j'},
	'i': {'u', 'o', 'j', 'k'},
	'o': {'i', 'p', 'k', 'l'},
	'p': {'o', 'l'},
	'a': {'q', 'w', 's', 'z'},
	's': {'a', 'w', 'e', 'd', 'z', 'x'},
	'd': {'s', 'e', 'r', 'f', 'x', 'c'},
	'f': {'d', 'r', 't', 'g', 'c', 'v'},
	'g': {'f', 't', 'y', 'h', 'v', 'b'},
	'h': {'g', 'y', 'u', 'j', 'b', 'n'},
	'j': {'h', 'u', 'i', 'k', 'n', 'm'},
	'k': {'j', 'i', 'o', 'l', 'm'},
	'l': {'k', 'o', 'p'},
	'z': {'a', 's', 'x'},
	'x': {'z', 's', 'd', 'c'},
	'c': {'x', 'd', 'f', 'v'},
	'v': {'c', 'f', 'g', 'b'},
	'b': {'v', 'g', 'h', 'n'},
	'n': {'b', 'h', 'j', 'm'},
	'm': {'n', 'j', 'k'},
	'1': {'2', 'q'},
	'2': {'1', '3', 'q', 'w'},
	'3': {'2', '4', 'w', 'e'},
	'4': {'3', '5', 'e', 'r'},
	'5': {'4', '6', 'r', 't'},
	'6': {'5', '7', 't', 'y'},
	'7': {'6', '8', 'y', 'u'},
	'8': {'7', '9', 'u', 'i'},
	'9': {'8', '0', 'i', 'o'},
	'0': {'9', 'o', 'p'},
}

func randomNeighbor(r rune) (rune, bool) {
	lower := unicode.ToLower(r)
	neighbors, ok := qwertyNeighbors[lower]
	if !ok || len(neighbors) == 0 {
		return 0, false
	}
	n := neighbors[rand.IntN(len(neighbors))]
	if unicode.IsUpper(r) {
		n = unicode.ToUpper(n)
	}
	return n, true
}

// ---- typing simulation ----

func typingDelay(r rune) time.Duration {
	base := 20 + rand.IntN(25)

	switch r {
	case ' ':
		base += 10 + rand.IntN(20)
	case '.', '!', '?':
		base += 100 + rand.IntN(150)
	case ',', ';', ':':
		base += 30 + rand.IntN(40)
	case '\n':
		base += 150 + rand.IntN(200)
	}

	jitter := rand.IntN(20) - 10
	return time.Duration(base+jitter) * time.Millisecond
}

func typeText(kb *Keyboard, text string, typoRate float64) {
	runes := []rune(text)
	for i, r := range runes {
		if rand.Float64() < typoRate {
			if wrong, ok := randomNeighbor(r); ok {
				kb.sendChar(wrong)
				time.Sleep(time.Duration(300+rand.IntN(300)) * time.Millisecond)
				kb.sendBackspace()
				time.Sleep(time.Duration(100+rand.IntN(80)) * time.Millisecond)
			}
		}

		if err := kb.sendChar(r); err != nil {
			fmt.Fprintf(os.Stderr, "sendChar error at position %d: %v\n", i, err)
		}

		time.Sleep(typingDelay(r))
	}
}

// ---- clipboard ----

func getClipboard() (string, error) {
	out, err := exec.Command("wl-paste", "--no-newline").Output()
	if err == nil {
		return string(out), nil
	}
	out, err = exec.Command("xclip", "-selection", "clipboard", "-o").Output()
	if err == nil {
		return string(out), nil
	}
	return "", fmt.Errorf("clipboard unavailable (tried wl-paste and xclip)")
}

// ---- main ----

func countdown(d time.Duration) {
	remaining := d
	for remaining > 0 {
		secs := max(int(remaining.Seconds()+0.5), 1)
		fmt.Fprintf(os.Stderr, "\rTyping starts in %d... ", secs)
		step := min(remaining, time.Second)
		time.Sleep(step)
		remaining -= step
	}
	fmt.Fprintln(os.Stderr)
}

func main() {
	rateFlag := flag.Float64("rate", defaultTypoRate, "Typo rate (0.0 to 1.0)")
	delayFlag := flag.Duration("delay", 2500*time.Millisecond, "Delay before typing starts")
	flag.Parse()

	kb, err := NewKeyboard()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to create virtual keyboard:", err)
		fmt.Fprintln(os.Stderr, "Make sure /dev/uinput is accessible (run with --privileged or --device /dev/uinput)")
		os.Exit(1)
	}
	defer kb.Close()

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(os.Stderr, "auto_typer ready.")
	fmt.Fprintln(os.Stderr, "Copy text to clipboard, then press Enter to type it out.")
	fmt.Fprintln(os.Stderr, "Type 'q' + Enter to quit.")
	fmt.Fprintln(os.Stderr)

	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "q" || line == "quit" || line == "exit" {
			fmt.Fprintln(os.Stderr, "Bye.")
			break
		}

		text, err := getClipboard()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not read clipboard:", err)
			continue
		}
		text = strings.TrimRight(text, "\n")
		if text == "" {
			fmt.Fprintln(os.Stderr, "Clipboard is empty, copy something first.")
			continue
		}

		fmt.Fprintf(os.Stderr, "Will type %d characters. Switch to target window now!\n", len([]rune(text)))
		countdown(*delayFlag)
		typeText(kb, text, *rateFlag)
		fmt.Fprintln(os.Stderr, "Done.")
		fmt.Fprintln(os.Stderr)
	}
}
