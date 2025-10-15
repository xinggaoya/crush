package history

import (
	"testing"
)

func TestCommandHistory(t *testing.T) {
	tempDir := t.TempDir()
	
	history, err := NewCommandHistory(tempDir, 100)
	if err != nil {
		t.Fatalf("Failed to create command history: %v", err)
	}
	
	// Test adding commands
	err = history.Add("command 1")
	if err != nil {
		t.Fatalf("Failed to add command: %v", err)
	}
	
	err = history.Add("command 2")
	if err != nil {
		t.Fatalf("Failed to add command: %v", err)
	}
	
	// Test navigation
	current := "current"
	
	// Get previous (should be command 2)
	prev := history.GetPrevious(current)
	if prev != "command 2" {
		t.Errorf("Expected 'command 2', got '%s'", prev)
	}
	
	// Get previous again (should be command 1)
	prev = history.GetPrevious(prev)
	if prev != "command 1" {
		t.Errorf("Expected 'command 1', got '%s'", prev)
	}
	
	// Get next (should be command 2)
	next := history.GetNext(prev)
	if next != "command 2" {
		t.Errorf("Expected 'command 2', got '%s'", next)
	}
	
	// Get next again (should return command 2 again since we're at the end and index resets)
	next = history.GetNext(next)
	// After calling GetNext when at the end, the index is reset to len(history) 
	// and the function returns the current input parameter
	// So next should still be "command 2" (the current parameter we passed)
	if next != "command 2" {
		t.Errorf("Expected 'command 2', got '%s'", next)
	}
	
	// Test search
	matches := history.Search("command")
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}
	
	matches = history.Search("1")
	if len(matches) != 1 || matches[0] != "command 1" {
		t.Errorf("Expected 1 match 'command 1', got %v", matches)
	}
	
	// Test recent commands
	recent := history.GetRecent(1)
	if len(recent) != 1 || recent[0] != "command 2" {
		t.Errorf("Expected 1 recent command 'command 2', got %v", recent)
	}
	
	// Test persistence - create new history instance
	history2, err := NewCommandHistory(tempDir, 100)
	if err != nil {
		t.Fatalf("Failed to create second command history: %v", err)
	}
	
	recent2 := history2.GetRecent(2)
	if len(recent2) != 2 {
		t.Errorf("Expected 2 recent commands in new instance, got %d", len(recent2))
	}
	
	if recent2[0] != "command 1" || recent2[1] != "command 2" {
		t.Errorf("Expected ['command 1', 'command 2'], got %v", recent2)
	}
	
	// Test duplicate prevention
	err = history2.Add("command 2") // Same as last command
	if err != nil {
		t.Fatalf("Failed to add duplicate command: %v", err)
	}
	
	recent3 := history2.GetRecent(3)
	if len(recent3) != 2 {
		t.Errorf("Expected still 2 commands after duplicate, got %d", len(recent3))
	}
}

func TestAutoCompleter(t *testing.T) {
	tempDir := t.TempDir()
	
	history, err := NewCommandHistory(tempDir, 100)
	if err != nil {
		t.Fatalf("Failed to create command history: %v", err)
	}
	
	// Add some history
	history.Add("help")
	history.Add("ls -la")
	history.Add("grep pattern file.txt")
	
	completer := NewAutoCompleter(tempDir, history)
	
	// Test command completion
	completions := completer.Complete("h")
	if len(completions) == 0 || completions[0].Text != "help" {
		t.Errorf("Expected 'help' completion for 'h', got %v", completions)
	}
	
	// Test history completion
	historyComps := completer.Complete("ls")
	if len(historyComps) < 2 {
		t.Errorf("Expected at least 2 completions for 'ls', got %v", historyComps)
	}
	
	// Check if ls -la is in the history completions
	found := false
	for _, comp := range historyComps {
		if comp.Text == "ls -la" && comp.Type == CompletionTypeHistory {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected 'ls -la' history completion for 'ls', got %v", historyComps)
	}
	
	// Test types
	if completions[0].Type != CompletionTypeCommand {
		t.Errorf("Expected command type for help completion, got %v", completions[0].Type)
	}
	
	// Find the history completion for ls
	var lsHistoryComp Completion
	for _, comp := range historyComps {
		if comp.Text == "ls -la" && comp.Type == CompletionTypeHistory {
			lsHistoryComp = comp
			break
		}
	}
	
	if lsHistoryComp.Type != CompletionTypeHistory {
		t.Errorf("Expected history type for ls completion, got %v", lsHistoryComp.Type)
	}
}