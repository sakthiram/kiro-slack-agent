package kiro

import (
	"regexp"
	"strings"
)

// Parser processes raw PTY output from Kiro CLI.
type Parser struct {
	// ANSI escape sequences regex - matches all terminal control sequences
	ansiRegex *regexp.Regexp
}

// NewParser creates a new output parser.
func NewParser() *Parser {
	return &Parser{
		// Comprehensive ANSI escape sequence pattern:
		// - CSI sequences: ESC[...X (colors, cursor, etc.)
		// - Private mode: ESC[?...X (cursor visibility, bracketed paste)
		// - OSC sequences: ESC]...BEL or ESC]...ST
		// - Single-char escapes: ESC7, ESC8, ESC=, ESC>
		// - Character set: ESC(X, ESC)X
		ansiRegex: regexp.MustCompile(`\x1b(?:\[\??[0-9;]*[a-zA-Z]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[78=>]|[\(\)][0-9A-Za-z])`),
	}
}

// ParseResult contains the parsed output.
type ParseResult struct {
	CleanText  string // Cleaned text content
	IsComplete bool   // Whether response appears complete (prompt detected)
	HasContent bool   // Whether there's meaningful content
}

// Parse processes raw PTY output and returns clean text.
func (p *Parser) Parse(rawOutput []byte) *ParseResult {
	text := string(rawOutput)

	// Step 1: Remove all ANSI escape sequences
	text = p.ansiRegex.ReplaceAllString(text, "")

	// Step 2: Remove control characters (except newline and tab)
	text = removeControlChars(text)

	// Step 3: Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Step 4: Check for completion (prompt at end)
	isComplete := detectPrompt(text)

	// Step 5: Extract AI response
	cleanText := extractResponse(text)

	return &ParseResult{
		CleanText:  cleanText,
		IsComplete: isComplete,
		HasContent: len(cleanText) > 0,
	}
}

// removeControlChars removes control characters except newline and tab.
func removeControlChars(text string) string {
	var result strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\t' || r >= 32 {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// detectPrompt checks if the output ends with a kiro-cli prompt.
func detectPrompt(text string) bool {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// Prompt patterns: "[profile] > " or just "> " at end of line
		if strings.HasSuffix(line, "> ") || line == ">" {
			return true
		}
		// Also check for prompt without trailing space
		if strings.HasSuffix(line, "]>") || strings.HasSuffix(line, "] >") {
			return true
		}
		return false
	}
	return false
}

// extractResponse extracts the AI response from the PTY output.
func extractResponse(text string) string {
	// The AI response is prefixed with "> " in kiro-cli output
	// Find the first "> " that starts a response (after any spinners/thinking)

	// First, collapse multiple whitespace/newlines
	text = strings.TrimSpace(text)

	// Look for "> " pattern that indicates the AI response
	responseIdx := strings.Index(text, "> ")
	if responseIdx >= 0 {
		// Extract everything after "> " up to the timing line or prompt
		response := text[responseIdx+2:] // Skip the "> " prefix

		// Find where to stop (timing line or prompt)
		// Look for "▸ Time:" or "[" followed by prompt
		if timeIdx := strings.Index(response, "▸ Time:"); timeIdx >= 0 {
			response = response[:timeIdx]
		}
		if timeIdx := strings.Index(response, "\n▸"); timeIdx >= 0 {
			response = response[:timeIdx]
		}

		// Clean up the response
		response = strings.TrimSpace(response)

		// Remove any trailing prompt patterns
		lines := strings.Split(response, "\n")
		var cleanLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if isPromptLine(trimmed) {
				continue
			}
			if isSpinnerLine(trimmed) {
				continue
			}
			cleanLines = append(cleanLines, line)
		}

		return strings.TrimSpace(strings.Join(cleanLines, "\n"))
	}

	// No "> " found - return the raw text without spinners/prompts
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isSpinnerLine(trimmed) || isPromptLine(trimmed) {
			continue
		}
		if strings.HasPrefix(trimmed, "▸ Time:") {
			continue
		}
		cleanLines = append(cleanLines, line)
	}

	return strings.TrimSpace(strings.Join(cleanLines, "\n"))
}

// isSpinnerLine checks if a line is a spinner/progress indicator.
func isSpinnerLine(line string) bool {
	// Braille spinner patterns and "Thinking..." text
	spinnerPrefixes := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", "⢀", "⡀", "⠄", "⢂", "⡂", "⠅", "⢃", "⡃", "⠍", "⢋", "⡋"}
	for _, prefix := range spinnerPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	// Lines that are just "Thinking..." or similar
	if strings.Contains(line, "Thinking...") {
		return true
	}
	// Lines that are just spinner characters
	if len(line) > 0 && len(line) <= 3 && containsOnlySpinnerChars(line) {
		return true
	}
	return false
}

// containsOnlySpinnerChars checks if a string contains only braille spinner characters.
func containsOnlySpinnerChars(s string) bool {
	for _, r := range s {
		if r < 0x2800 || r > 0x28FF { // Braille pattern Unicode block
			if r != '|' && r != '/' && r != '-' && r != '\\' && r != ' ' {
				return false
			}
		}
	}
	return true
}

// isPromptLine checks if a line is a kiro-cli prompt.
func isPromptLine(line string) bool {
	// Patterns like "[amelia_agent] > " or "[profile] >"
	if strings.HasPrefix(line, "[") && (strings.HasSuffix(line, "> ") || strings.HasSuffix(line, ">") || strings.HasSuffix(line, "] >")) {
		return true
	}
	// Just "> " or ">" alone
	if line == ">" || line == "> " {
		return true
	}
	return false
}

// RemoveANSI is a convenience function to just strip ANSI codes.
func RemoveANSI(text string) string {
	p := NewParser()
	return p.ansiRegex.ReplaceAllString(text, "")
}
