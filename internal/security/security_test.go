package security

import (
	"testing"
)

func TestSecureStorage(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()
	
	storage, err := NewSecureStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create secure storage: %v", err)
	}
	
	// Test encryption and decryption
	plaintext := "test-api-key-12345"
	
	ciphertext, err := storage.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}
	
	if ciphertext == plaintext {
		t.Error("Ciphertext should be different from plaintext")
	}
	
	// Test decryption
	decrypted, err := storage.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}
	
	if decrypted != plaintext {
		t.Errorf("Decrypted text doesn't match original. Got %s, want %s", decrypted, plaintext)
	}
	
	// Test persistence - create new storage instance and test decryption
	storage2, err := NewSecureStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create second secure storage: %v", err)
	}
	
	decrypted2, err := storage2.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Failed to decrypt with new storage instance: %v", err)
	}
	
	if decrypted2 != plaintext {
		t.Errorf("Decrypted text doesn't match original with new instance. Got %s, want %s", decrypted2, plaintext)
	}
}

func TestAuditLogger(t *testing.T) {
	audit := NewAuditLogger()
	
	// Test logging events
	audit.LogEvent("test_action", "test_resource", "test_user", true, "test details")
	audit.LogEvent("test_action", "test_resource", "test_user", false, "test error")
	
	// Test retrieving recent events
	events := audit.GetRecentEvents(10)
	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}
	
	// Check first event
	if events[0].Action != "test_action" {
		t.Errorf("Expected action 'test_action', got '%s'", events[0].Action)
	}
	
	if events[0].Success != true {
		t.Errorf("Expected success true, got %v", events[0].Success)
	}
	
	// Check second event
	if events[1].Success != false {
		t.Errorf("Expected success false, got %v", events[1].Success)
	}
}