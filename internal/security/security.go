package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

// SecureStorage provides encrypted storage for sensitive data like API keys
type SecureStorage struct {
	mu       sync.RWMutex
	gcm      cipher.AEAD
	keyFile  string
	saltFile string
}

// NewSecureStorage creates a new secure storage instance
func NewSecureStorage(dataDir string) (*SecureStorage, error) {
	keyFile := fmt.Sprintf("%s/.key", dataDir)
	saltFile := fmt.Sprintf("%s/.salt", dataDir)

	// Generate or load key
	key, err := loadOrGenerateKey(keyFile, saltFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load or generate key: %w", err)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	return &SecureStorage{
		gcm:      gcm,
		keyFile:  keyFile,
		saltFile: saltFile,
	}, nil
}

// Encrypt encrypts the given plaintext
func (s *SecureStorage) Encrypt(plaintext string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := s.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts the given ciphertext
func (s *SecureStorage) Decrypt(ciphertext string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	nonceSize := s.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext_bytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := s.gcm.Open(nil, nonce, ciphertext_bytes, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// loadOrGenerateKey loads existing key or generates a new one
func loadOrGenerateKey(keyFile, saltFile string) ([]byte, error) {
	// Try to load existing key and salt
	if key, salt, err := loadExistingKey(keyFile, saltFile); err == nil {
		return deriveKey(key, salt), nil
	}

	// Generate new key and salt
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Save key and salt
	if err := os.WriteFile(keyFile, key, 0o600); err != nil {
		return nil, fmt.Errorf("failed to save key: %w", err)
	}

	if err := os.WriteFile(saltFile, salt, 0o600); err != nil {
		return nil, fmt.Errorf("failed to save salt: %w", err)
	}

	return deriveKey(key, salt), nil
}

func loadExistingKey(keyFile, saltFile string) ([]byte, []byte, error) {
	key, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, err
	}

	salt, err := os.ReadFile(saltFile)
	if err != nil {
		return nil, nil, err
	}

	return key, salt, nil
}

func deriveKey(key, salt []byte) []byte {
	hash := sha256.New()
	hash.Write(key)
	hash.Write(salt)
	hash.Write([]byte(runtime.GOOS)) // Add OS-specific salt
	return hash.Sum(nil)
}

// AuditLogger provides structured audit logging for security events
type AuditLogger struct {
	mu     sync.Mutex
	events []AuditEvent
}

type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	User      string    `json:"user"`
	Success   bool      `json:"success"`
	Details   string    `json:"details,omitempty"`
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{
		events: make([]AuditEvent, 0, 1000),
	}
}

// LogEvent logs a security event
func (a *AuditLogger) LogEvent(action, resource, user string, success bool, details string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	event := AuditEvent{
		Timestamp: time.Now().UTC(),
		Action:    action,
		Resource:  resource,
		User:      user,
		Success:   success,
		Details:   details,
	}

	a.events = append(a.events, event)

	// Keep only last 1000 events
	if len(a.events) > 1000 {
		a.events = a.events[1:]
	}
}

// GetRecentEvents returns recent audit events
func (a *AuditLogger) GetRecentEvents(limit int) []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()

	if limit > len(a.events) {
		limit = len(a.events)
	}

	start := len(a.events) - limit
	return a.events[start:]
}
