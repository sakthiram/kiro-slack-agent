package kiro

import (
	"regexp"
	"strings"
)

// Parser processes raw PTY output from Kiro CLI.
type Parser struct {
	ansiRegex     *regexp.Regexp
	oscRegex      *regexp.Regexp
	promptRegex   *regexp.Regexp
	spinnerRegex  *regexp.Regexp
	controlRegex  *regexp.Regexp
}

// NewParser creates a new output parser.
func NewParser() *Parser {
	return &Parser{
		// ANSI escape sequences: colors, cursor movement, etc.
		// Matches: ESC[...m, ESC[...H, ESC[...J, etc.
		ansiRegex: regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`),

		// OSC (Operating System Command) sequences: ESC]...BEL
		oscRegex: regexp.MustCompile(`\x1b\][^\x07]*\x07`),

		// Kiro prompt patterns (including assistant> which kiro-cli uses)
		promptRegex: regexp.MustCompile(`(?m)^(>|\$|kiro>|claude>|assistant>)\s*$`),

		// Spinner/progress characters that might appear
		spinnerRegex: regexp.MustCompile(`^[|/\-\\⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]+\s*$`),

		// Control characters (except newline and tab)
		controlRegex: regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`),
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

	// Step 1: Remove OSC sequences (terminal title, etc.)
	text = p.oscRegex.ReplaceAllString(text, "")

	// Step 2: Remove ANSI escape sequences
	text = p.ansiRegex.ReplaceAllString(text, "")

	// Step 3: Remove other control characters
	text = p.controlRegex.ReplaceAllString(text, "")

	// Step 4: Normalize line endings
	text = normalizeLineEndings(text)

	// Step 5: Check for completion (prompt at end)
	isComplete := p.detectCompletion(text)

	// Step 6: Remove prompt lines and spinner lines
	text = p.cleanContent(text)

	// Step 7: Final cleanup
	text = strings.TrimSpace(text)

	return &ParseResult{
		CleanText:  text,
		IsComplete: isComplete,
		HasContent: len(text) > 0,
	}
}

// normalizeLineEndings converts various line endings to \n.
func normalizeLineEndings(text string) string {
	// Replace \r\n with \n
	text = strings.ReplaceAll(text, "\r\n", "\n")
	// Replace standalone \r (carriage return without newline)
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// detectCompletion checks if the output ends with a prompt.
// Only returns true if the last non-empty line is a prompt.
func (p *Parser) detectCompletion(text string) bool {
	lines := strings.Split(text, "\n")
	// Find the last non-empty line
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// Check if this last non-empty line is a prompt
		return p.promptRegex.MatchString(line)
	}
	return false
}

// cleanContent removes prompt lines and spinner artifacts.
func (p *Parser) cleanContent(text string) string {
	lines := strings.Split(text, "\n")
	var cleaned []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines at start
		if len(cleaned) == 0 && trimmed == "" {
			continue
		}

		// Skip prompt lines
		if p.promptRegex.MatchString(trimmed) {
			continue
		}

		// Skip spinner-only lines
		if p.spinnerRegex.MatchString(trimmed) {
			continue
		}

		cleaned = append(cleaned, line)
	}

	// Remove trailing empty lines
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}

	return strings.Join(cleaned, "\n")
}

// RemoveANSI is a convenience function to just strip ANSI codes.
func RemoveANSI(text string) string {
	p := NewParser()
	text = p.oscRegex.ReplaceAllString(text, "")
	text = p.ansiRegex.ReplaceAllString(text, "")
	return text
}
