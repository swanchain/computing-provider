package setup

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Colors for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

// Symbols for output
const (
	symbolCheck  = "[ok]"
	symbolCross  = "[x]"
	symbolArrow  = "->"
	symbolBullet = "*"
)

// Prompter handles interactive CLI prompts
type Prompter struct {
	reader *bufio.Reader
}

// NewPrompter creates a new prompter
func NewPrompter() *Prompter {
	return &Prompter{
		reader: bufio.NewReader(os.Stdin),
	}
}

// AskString prompts for a string input
func (p *Prompter) AskString(prompt string, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}

	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

// AskPassword prompts for a password (hidden input)
func (p *Prompter) AskPassword(prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)

	// Read password without echo
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", err
	}
	fmt.Println() // Add newline after password input

	return string(bytePassword), nil
}

// AskYesNo prompts for a yes/no answer
func (p *Prompter) AskYesNo(prompt string, defaultYes bool) (bool, error) {
	defaultStr := "y/N"
	if defaultYes {
		defaultStr = "Y/n"
	}

	fmt.Printf("%s [%s]: ", prompt, defaultStr)

	input, err := p.reader.ReadString('\n')
	if err != nil {
		return false, err
	}

	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return defaultYes, nil
	}

	return input == "y" || input == "yes", nil
}

// SelectionOption represents a selection option
type SelectionOption struct {
	Label       string
	Description string
	Value       interface{}
}

// AskSelection prompts user to select one option from a list
func (p *Prompter) AskSelection(prompt string, options []SelectionOption) (int, error) {
	fmt.Println(prompt)
	for i, opt := range options {
		if opt.Description != "" {
			fmt.Printf("  %d) %s - %s\n", i+1, opt.Label, opt.Description)
		} else {
			fmt.Printf("  %d) %s\n", i+1, opt.Label)
		}
	}

	for {
		fmt.Printf("Enter selection (1-%d): ", len(options))
		input, err := p.reader.ReadString('\n')
		if err != nil {
			return -1, err
		}

		input = strings.TrimSpace(input)
		selection, err := strconv.Atoi(input)
		if err != nil || selection < 1 || selection > len(options) {
			fmt.Printf("Please enter a number between 1 and %d\n", len(options))
			continue
		}

		return selection - 1, nil
	}
}

// AskMultiSelect prompts user to select multiple options from a list
func (p *Prompter) AskMultiSelect(prompt string, options []SelectionOption) ([]int, error) {
	fmt.Println(prompt)
	for i, opt := range options {
		if opt.Description != "" {
			fmt.Printf("  %d) %s - %s\n", i+1, opt.Label, opt.Description)
		} else {
			fmt.Printf("  %d) %s\n", i+1, opt.Label)
		}
	}

	fmt.Printf("Enter selections separated by comma (e.g., 1,3,4) or 'all': ")
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	input = strings.TrimSpace(strings.ToLower(input))
	if input == "all" {
		result := make([]int, len(options))
		for i := range options {
			result[i] = i
		}
		return result, nil
	}

	var selected []int
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		num, err := strconv.Atoi(part)
		if err != nil || num < 1 || num > len(options) {
			continue
		}
		selected = append(selected, num-1)
	}

	return selected, nil
}

// PrintHeader prints a styled header
func PrintHeader(title string) {
	width := 60
	fmt.Println()
	fmt.Printf("%s%s%s\n", colorCyan, strings.Repeat("=", width), colorReset)
	padding := (width - len(title)) / 2
	fmt.Printf("%s%s%s%s%s\n", colorCyan, strings.Repeat(" ", padding), colorBold, title, colorReset)
	fmt.Printf("%s%s%s\n", colorCyan, strings.Repeat("=", width), colorReset)
	fmt.Println()
}

// PrintStep prints a step header
func PrintStep(step, total int, title string) {
	fmt.Println()
	fmt.Printf("%sStep %d/%d: %s%s\n", colorBold, step, total, title, colorReset)
	fmt.Println(strings.Repeat("-", 60))
}

// PrintSuccess prints a success message
func PrintSuccess(msg string) {
	fmt.Printf("%s%s%s %s\n", colorGreen, symbolCheck, colorReset, msg)
}

// PrintError prints an error message
func PrintError(msg string) {
	fmt.Printf("%s%s%s %s\n", colorRed, symbolCross, colorReset, msg)
}

// PrintWarning prints a warning message
func PrintWarning(msg string) {
	fmt.Printf("%s[!]%s %s\n", colorYellow, colorReset, msg)
}

// PrintInfo prints an info message
func PrintInfo(msg string) {
	fmt.Printf("%s%s%s %s\n", colorBlue, symbolArrow, colorReset, msg)
}

// PrintBullet prints a bullet point
func PrintBullet(msg string) {
	fmt.Printf("  %s %s\n", symbolBullet, msg)
}

// PrintKeyValue prints a key-value pair
func PrintKeyValue(key, value string) {
	fmt.Printf("  %s%-20s%s %s\n", colorDim, key+":", colorReset, value)
}

// Spinner represents a progress spinner
type Spinner struct {
	message string
	done    chan bool
	active  bool
}

// NewSpinner creates a new spinner
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message: message,
		done:    make(chan bool),
	}
}

// Start starts the spinner
func (s *Spinner) Start() {
	s.active = true
	go func() {
		frames := []string{"|", "/", "-", "\\"}
		i := 0
		for s.active {
			select {
			case <-s.done:
				return
			default:
				fmt.Printf("\r%s %s", frames[i], s.message)
				i = (i + 1) % len(frames)
				// Small delay - this is a simple spinner
				for j := 0; j < 10000000 && s.active; j++ {
				}
			}
		}
	}()
}

// Stop stops the spinner and clears the line
func (s *Spinner) Stop() {
	s.active = false
	s.done <- true
	fmt.Printf("\r%s\r", strings.Repeat(" ", len(s.message)+10))
}

// StopWithSuccess stops the spinner and prints success
func (s *Spinner) StopWithSuccess(msg string) {
	s.Stop()
	PrintSuccess(msg)
}

// StopWithError stops the spinner and prints error
func (s *Spinner) StopWithError(msg string) {
	s.Stop()
	PrintError(msg)
}
