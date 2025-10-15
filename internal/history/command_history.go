package history

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CommandHistory manages command history and completion
type CommandHistory struct {
	mu      sync.RWMutex
	history []string
	index   int
	file    string
	maxSize int
}

// NewCommandHistory creates a new command history manager
func NewCommandHistory(dataDir string, maxSize int) (*CommandHistory, error) {
	if maxSize <= 0 {
		maxSize = 1000 // Default max size
	}

	historyFile := filepath.Join(dataDir, "command_history.txt")

	ch := &CommandHistory{
		history: make([]string, 0, maxSize),
		index:   -1,
		file:    historyFile,
		maxSize: maxSize,
	}

	// Load existing history
	if err := ch.loadHistory(); err != nil {
		return nil, fmt.Errorf("failed to load command history: %w", err)
	}

	return ch, nil
}

// Add adds a command to history
func (ch *CommandHistory) Add(command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	// Don't add duplicate consecutive commands
	if len(ch.history) > 0 && ch.history[len(ch.history)-1] == command {
		return nil
	}

	ch.history = append(ch.history, command)
	ch.index = len(ch.history)

	// Trim history if it exceeds max size
	if len(ch.history) > ch.maxSize {
		ch.history = ch.history[1:]
		ch.index--
	}

	// Save to file
	return ch.saveHistory()
}

// GetPrevious returns the previous command in history
func (ch *CommandHistory) GetPrevious(current string) string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if ch.index <= 0 {
		return current
	}

	ch.index--
	return ch.history[ch.index]
}

// GetNext returns the next command in history
func (ch *CommandHistory) GetNext(current string) string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if ch.index >= len(ch.history)-1 {
		ch.index = len(ch.history) // Reset to end
		return current
	}

	ch.index++
	return ch.history[ch.index]
}

// ResetIndex resets the history index to the end
func (ch *CommandHistory) ResetIndex() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.index = len(ch.history)
}

// Search searches for commands matching the pattern
func (ch *CommandHistory) Search(pattern string) []string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	var matches []string
	pattern = strings.ToLower(pattern)

	for i := len(ch.history) - 1; i >= 0; i-- {
		cmd := ch.history[i]
		if strings.Contains(strings.ToLower(cmd), pattern) {
			matches = append(matches, cmd)
		}
	}

	return matches
}

// GetRecent returns recent commands
func (ch *CommandHistory) GetRecent(count int) []string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	if count <= 0 {
		return nil
	}

	if count > len(ch.history) {
		count = len(ch.history)
	}

	start := len(ch.history) - count
	result := make([]string, count)
	copy(result, ch.history[start:])

	return result
}

// loadHistory loads command history from file
func (ch *CommandHistory) loadHistory() error {
	file, err := os.Open(ch.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No history file yet
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			ch.history = append(ch.history, line)
		}
	}

	ch.index = len(ch.history)
	return scanner.Err()
}

// saveHistory saves command history to file
func (ch *CommandHistory) saveHistory() error {
	file, err := os.Create(ch.file)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, cmd := range ch.history {
		if _, err := fmt.Fprintln(file, cmd); err != nil {
			return err
		}
	}

	return nil
}

// AutoCompleter provides auto-completion for commands and files
type AutoCompleter struct {
	commands []string
	files    []string
	history  *CommandHistory
	lastScan time.Time
	dataDir  string
}

// NewAutoCompleter creates a new auto-completer
func NewAutoCompleter(dataDir string, history *CommandHistory) *AutoCompleter {
	return &AutoCompleter{
		commands: []string{
			"help", "quit", "exit", "clear", "cls",
			"run", "edit", "view", "ls", "grep", "find",
			"save", "load", "delete", "copy", "move",
			"new session", "list sessions", "switch session",
			"export", "import", "settings", "config",
		},
		history: history,
		dataDir: dataDir,
	}
}

// Complete returns completions for the given input
func (ac *AutoCompleter) Complete(input string) []Completion {
	var completions []Completion

	// Get command completions
	for _, cmd := range ac.commands {
		if strings.HasPrefix(strings.ToLower(cmd), strings.ToLower(input)) {
			completions = append(completions, Completion{
				Text:        cmd,
				Type:        CompletionTypeCommand,
				Description: "Command",
			})
		}
	}

	// Get file completions if input looks like a path
	if strings.Contains(input, "/") || strings.Contains(input, "\\") {
		ac.scanFiles()
		for _, file := range ac.files {
			if strings.Contains(strings.ToLower(file), strings.ToLower(input)) {
				completions = append(completions, Completion{
					Text:        file,
					Type:        CompletionTypeFile,
					Description: "File",
				})
			}
		}
	}

	// Get history completions
	historyMatches := ac.history.Search(input)
	for _, match := range historyMatches {
		completions = append(completions, Completion{
			Text:        match,
			Type:        CompletionTypeHistory,
			Description: "History",
		})
	}

	return completions
}

// scanFiles scans current directory for files
func (ac *AutoCompleter) scanFiles() {
	// Scan at most once per minute
	if time.Since(ac.lastScan) < time.Minute {
		return
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		return
	}

	ac.files = ac.files[:0] // Clear slice but keep capacity
	for _, entry := range entries {
		if !entry.IsDir() {
			ac.files = append(ac.files, entry.Name())
		}
	}

	ac.lastScan = time.Now()
}

// CompletionType represents the type of completion
type CompletionType string

const (
	CompletionTypeCommand CompletionType = "command"
	CompletionTypeFile    CompletionType = "file"
	CompletionTypeHistory CompletionType = "history"
)

// Completion represents a completion suggestion
type Completion struct {
	Text        string
	Type        CompletionType
	Description string
}
