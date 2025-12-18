package kiro

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParser_RemoveANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no ansi codes",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "color codes",
			input: "\x1b[32mgreen text\x1b[0m",
			want:  "green text",
		},
		{
			name:  "cursor movement",
			input: "\x1b[H\x1b[2Jclear screen",
			want:  "clear screen",
		},
		{
			name:  "mixed content",
			input: "\x1b[1mbold\x1b[0m normal \x1b[31mred\x1b[0m",
			want:  "bold normal red",
		},
		{
			name:  "osc sequence (title)",
			input: "\x1b]0;Terminal Title\x07content",
			want:  "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveANSI(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParser_DetectCompletion(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "ends with >",
			input: "some output\n>",
			want:  true,
		},
		{
			name:  "ends with $",
			input: "some output\n$",
			want:  true,
		},
		{
			name:  "ends with kiro>",
			input: "some output\nkiro>",
			want:  true,
		},
		{
			name:  "ends with claude>",
			input: "some output\nclaude>",
			want:  true,
		},
		{
			name:  "ends with assistant>",
			input: "some output\nassistant>",
			want:  true,
		},
		{
			name:  "no prompt",
			input: "some output\nmore output",
			want:  false,
		},
		{
			name:  "prompt in middle",
			input: "some output\n>\nmore output",
			want:  false,
		},
		{
			name:  "prompt with trailing newline",
			input: "some output\n>\n",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.Parse([]byte(tt.input))
			assert.Equal(t, tt.want, result.IsComplete)
		})
	}
}

func TestParser_ExtractResponse(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple response",
			input: "Hello, I can help you with that.",
			want:  "Hello, I can help you with that.",
		},
		{
			name:  "response with prompt removed",
			input: "Hello, world!\n>",
			want:  "Hello, world!",
		},
		{
			name:  "multiline response",
			input: "Line 1\nLine 2\nLine 3",
			want:  "Line 1\nLine 2\nLine 3",
		},
		{
			name:  "response with ansi colors",
			input: "\x1b[32mSuccess:\x1b[0m Operation completed",
			want:  "Success: Operation completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.Parse([]byte(tt.input))
			assert.Equal(t, tt.want, result.CleanText)
		})
	}
}

func TestParser_HandleMultiline(t *testing.T) {
	p := NewParser()

	input := `Here is a code example:

` + "```go" + `
func main() {
    fmt.Println("Hello")
}
` + "```" + `

This should work!`

	result := p.Parse([]byte(input))

	assert.True(t, result.HasContent)
	assert.Contains(t, result.CleanText, "code example")
	assert.Contains(t, result.CleanText, "func main()")
	assert.Contains(t, result.CleanText, "This should work!")
}

func TestParser_HandleSpinners(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "spinner line removed",
			input: "|\nActual content",
			want:  "Actual content",
		},
		{
			name:  "unicode spinner removed",
			input: "⠋\nLoading done",
			want:  "Loading done",
		},
		{
			name:  "spinner in content preserved",
			input: "Progress: 50% | half done",
			want:  "Progress: 50% | half done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.Parse([]byte(tt.input))
			assert.Equal(t, tt.want, result.CleanText)
		})
	}
}

func TestParser_LineEndings(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "windows line endings",
			input: "line1\r\nline2",
			want:  "line1\nline2",
		},
		{
			name:  "old mac line endings",
			input: "line1\rline2",
			want:  "line1\nline2",
		},
		{
			name:  "unix line endings unchanged",
			input: "line1\nline2",
			want:  "line1\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.Parse([]byte(tt.input))
			assert.Equal(t, tt.want, result.CleanText)
		})
	}
}

func TestParser_EmptyInput(t *testing.T) {
	p := NewParser()

	result := p.Parse([]byte(""))
	assert.Equal(t, "", result.CleanText)
	assert.False(t, result.HasContent)
	assert.False(t, result.IsComplete)
}

func TestParser_OnlyWhitespace(t *testing.T) {
	p := NewParser()

	result := p.Parse([]byte("   \n\n   "))
	assert.Equal(t, "", result.CleanText)
	assert.False(t, result.HasContent)
}
