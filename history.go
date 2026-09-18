package main

// Registro de qué archivos .dbx se convirtieron con éxito. Guarda solo el
// nombre del archivo, la fecha y cuántos mensajes se extrajeron — nunca el
// contenido de los correos ni el .dbx en sí, que se siguen borrando apenas
// termina cada conversión.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type historyEntry struct {
	Filename     string    `json:"filename"`
	ConvertedAt  time.Time `json:"converted_at"`
	MessageCount int       `json:"message_count"`
}

type historyStore struct {
	mu   sync.Mutex
	path string
}

func newHistoryStore(path string) *historyStore {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0700)
	}
	return &historyStore{path: path}
}

func (h *historyStore) Append(filename string, messageCount int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entries := h.readLocked()
	entries = append(entries, historyEntry{
		Filename:     filename,
		ConvertedAt:  time.Now().UTC(),
		MessageCount: messageCount,
	})
	h.writeLocked(entries)
}

// List devuelve las entradas más recientes primero.
func (h *historyStore) List() []historyEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	entries := h.readLocked()
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

func (h *historyStore) readLocked() []historyEntry {
	data, err := os.ReadFile(h.path)
	if err != nil {
		return nil
	}
	var entries []historyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	return entries
}

func (h *historyStore) writeLocked(entries []historyEntry) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, h.path)
}
