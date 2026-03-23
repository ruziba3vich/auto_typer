package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

const defaultTypoRate = 0.07

// QWERTY keyboard neighbor map (lowercase only).
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

// sendChar types a single character using ydotool.
func sendChar(r rune) error {
	return exec.Command("ydotool", "type", "--key-delay", "0", "--next-delay", "0", "--", string(r)).Run()
}

// sendBackspace sends a backspace keystroke.
func sendBackspace() error {
	return exec.Command("ydotool", "key", "14:1", "14:0").Run() // KEY_BACKSPACE
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

func typingDelay(r rune) time.Duration {
	// Base: 20-45ms (fast typist)
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

	// Jitter: +/- 10ms
	jitter := rand.IntN(20) - 10
	return time.Duration(base+jitter) * time.Millisecond
}

func typeText(text string, typoRate float64) {
	runes := []rune(text)
	for i, r := range runes {
		// Decide if this character gets a typo.
		if rand.Float64() < typoRate {
			if wrong, ok := randomNeighbor(r); ok {
				// Type wrong character.
				sendChar(wrong)
				// Pause so the mistake is visible (300-600ms "oh wait" moment).
				time.Sleep(time.Duration(300+rand.IntN(300)) * time.Millisecond)

				// Backspace to erase.
				sendBackspace()
				time.Sleep(time.Duration(100+rand.IntN(80)) * time.Millisecond)
			}
		}

		// Type the correct character.
		if err := sendChar(r); err != nil {
			fmt.Fprintf(os.Stderr, "sendChar error at position %d: %v\n", i, err)
		}

		time.Sleep(typingDelay(r))
	}
}

func getClipboard() (string, error) {
	// Try wl-paste first (Wayland), fall back to xclip (X11).
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

		// Fetch clipboard content.
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
		typeText(text, *rateFlag)
		fmt.Fprintln(os.Stderr, "Done.")
		fmt.Fprintln(os.Stderr)
	}
}
