package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unsafe"
)

const (
	defaultTypoRate = 0.07
	defaultSpeed    = 1.0 // 1.0 = normal, 0.5 = twice as fast, 2.0 = twice as slow
)

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func envString(key string, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---- uinput constants ----

const (
	uiSetEvbit   = 0x40045564
	uiSetKeybit  = 0x40045565
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502

	evKey = 0x01
	evSyn = 0x00

	synReport = 0

	keyBackspace = 14
	keyTab       = 15
	keyEnter     = 28
	keyLeftShift = 42
	keySpace     = 57
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
	ID        [8]byte // input_id: bustype, vendor, product, version (4x uint16)
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

	if err := ioctl(fd, uiSetEvbit, evKey); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_SET_EVBIT EV_KEY: %w", err)
	}

	for i := uintptr(1); i <= 255; i++ {
		ioctl(fd, uiSetKeybit, i)
	}

	var dev uinputUserDev
	copy(dev.Name[:], "auto_typer virtual keyboard")
	binary.LittleEndian.PutUint16(dev.ID[0:2], 3)
	binary.LittleEndian.PutUint16(dev.ID[2:4], 0x1234)
	binary.LittleEndian.PutUint16(dev.ID[4:6], 0x5678)
	binary.LittleEndian.PutUint16(dev.ID[6:8], 1)

	devBytes := (*[unsafe.Sizeof(dev)]byte)(unsafe.Pointer(&dev))[:]
	if _, err := f.Write(devBytes); err != nil {
		f.Close()
		return nil, fmt.Errorf("write uinput_user_dev: %w", err)
	}

	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	return &Keyboard{f: f}, nil
}

func (kb *Keyboard) Close() {
	ioctl(kb.f.Fd(), uiDevDestroy, 0)
	kb.f.Close()
}

func (kb *Keyboard) emit(typ uint16, code uint16, value int32) error {
	ev := inputEvent{Type: typ, Code: code, Value: value}
	buf := (*[unsafe.Sizeof(ev)]byte)(unsafe.Pointer(&ev))[:]
	_, err := kb.f.Write(buf)
	return err
}

func (kb *Keyboard) sync() error {
	return kb.emit(evSyn, synReport, 0)
}

func (kb *Keyboard) tap(code uint16) error {
	if err := kb.emit(evKey, code, 1); err != nil {
		return err
	}
	if err := kb.emit(evKey, code, 0); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	return nil
}

func (kb *Keyboard) shiftTap(code uint16) error {
	if err := kb.emit(evKey, keyLeftShift, 1); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)

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

	if err := kb.emit(evKey, keyLeftShift, 0); err != nil {
		return err
	}
	if err := kb.sync(); err != nil {
		return err
	}
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
	if unicode.IsUpper(r) {
		if code, ok := keycodes[unicode.ToLower(r)]; ok {
			return kb.shiftTap(code)
		}
	}
	if code, ok := keycodes[r]; ok {
		return kb.tap(code)
	}
	if base, ok := shiftedKeys[r]; ok {
		if code, ok := keycodes[base]; ok {
			return kb.shiftTap(code)
		}
	}
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

func typingDelay(r rune, speed float64) time.Duration {
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
	ms := float64(base+jitter) * speed
	return time.Duration(ms) * time.Millisecond
}

// Auto-close pairs: when we type the opening char, editors insert the closing char.
var autoClosePairs = map[rune]rune{
	'{': '}',
	'[': ']',
	'(': ')',
	'"': '"',
	'\'': '\'',
}

func typeText(kb *Keyboard, text string, typoRate float64, speed float64) {
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Handle auto-close: after typing an opening bracket/quote,
		// the editor inserts the closing char. Delete it immediately.
		if _, isAutoClose := autoClosePairs[r]; isAutoClose {
			kb.sendChar(r)
			time.Sleep(10 * time.Millisecond)
			// Forward-delete the auto-inserted closing char.
			kb.tap(111) // KEY_DELETE (forward-delete)
			time.Sleep(5 * time.Millisecond)
			time.Sleep(typingDelay(r, speed))
			continue
		}

		// Handle newlines: type Enter, then clean up auto-indent.
		// Strategy: Home → End → Shift+Home selects all auto-content on the line.
		// Then typing the first real character replaces the selection.
		// If editor added nothing, selection is empty and typing just works.
		if r == '\n' {
			kb.sendChar('\n')
			time.Sleep(30 * time.Millisecond)

			// Escape to dismiss any autocomplete popups.
			kb.tap(1) // KEY_ESC
			time.Sleep(10 * time.Millisecond)

			// End (move past any auto-inserted content).
			kb.tap(107) // KEY_END
			time.Sleep(10 * time.Millisecond)

			// Shift+Home to select everything back to col 0.
			kb.emit(evKey, keyLeftShift, 1)
			kb.sync()
			time.Sleep(5 * time.Millisecond)
			kb.tap(102) // KEY_HOME
			time.Sleep(5 * time.Millisecond)
			kb.emit(evKey, keyLeftShift, 0)
			kb.sync()
			time.Sleep(10 * time.Millisecond)

			// Now any auto-indent/auto-close is selected.
			// The next character we type will replace the selection.
			// If nothing was selected, typing just inserts normally.

			time.Sleep(typingDelay('\n', speed))

			// Type the actual leading whitespace from the source.
			// The first character typed replaces any selection.
			if i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\t') {
				for i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '\t') {
					i++
					kb.sendChar(runes[i])
					time.Sleep(2 * time.Millisecond)
				}
			} else if i+1 < len(runes) && runes[i+1] != '\n' {
				// Next char is non-whitespace — type it to replace selection.
				i++
				kb.sendChar(runes[i])
				time.Sleep(typingDelay(runes[i], speed))
			}
			// If next line is empty (\n\n), selection is empty, nothing to do.
			continue
		}

		// Typo simulation (not on whitespace).
		if r != ' ' && r != '\t' && rand.Float64() < typoRate {
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

		time.Sleep(typingDelay(r, speed))
	}
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
	fileFlag := flag.String("file", envString("FILE", "/texts/main.txt"), "Path to text file to type")
	rateFlag := flag.Float64("rate", envFloat("TYPO_RATE", defaultTypoRate), "Typo rate (0.0 to 1.0)")
	speedFlag := flag.Float64("speed", envFloat("TYPING_SPEED", defaultSpeed), "Typing speed multiplier (0.5=fast, 1.0=normal, 2.0=slow)")
	delayFlag := flag.Duration("delay", envDuration("TYPING_DELAY", 2500*time.Millisecond), "Delay before typing starts")
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
	if *fileFlag != "" {
		fmt.Fprintf(os.Stderr, "File: %s\n", *fileFlag)
	}
	fmt.Fprintln(os.Stderr, "Press Enter to start typing (switch to target window within the delay).")
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

		var text string

		// Allow changing file at runtime: type a path to use that file.
		inputFile := *fileFlag
		if line != "" {
			inputFile = line
		}

		if inputFile != "" {
			data, err := os.ReadFile(inputFile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Could not read file:", err)
				continue
			}
			text = string(data)
		} else {
			fmt.Fprintln(os.Stderr, "No file specified. Pass -file flag or type a file path.")
			continue
		}

		text = strings.TrimRight(text, "\n")
		if text == "" {
			fmt.Fprintln(os.Stderr, "File is empty.")
			continue
		}

		fmt.Fprintf(os.Stderr, "Will type %d characters. Switch to target window now!\n", len([]rune(text)))
		countdown(*delayFlag)
		typeText(kb, text, *rateFlag, *speedFlag)
		fmt.Fprintln(os.Stderr, "Done.")
		fmt.Fprintln(os.Stderr)
	}
}
