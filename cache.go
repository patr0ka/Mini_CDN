package main

import (
	// sync.Mutex impede que duas requisições alterem o mapa ao mesmo tempo.
	"sync"
	// time mede quando cada cópia deve expirar.
	"time"
)

// cacheEntry guarda o conteúdo copiado e o instante em que ele deixa de ser válido.
type cacheEntry struct {
	body        []byte
	contentType string
	expiresAt   time.Time
}

// memoryCache é um cache simples que vive na memória de um processo edge.
type memoryCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// newMemoryCache cria o mapa vazio e grava o TTL configurado na inicialização.
func newMemoryCache(ttl time.Duration) *memoryCache {
	return &memoryCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// get procura uma cópia. O booleano final é true apenas quando a cópia ainda é válida.
func (c *memoryCache) get(key string) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, found := c.entries[key]
	if !found {
		return nil, "", false
	}
	if time.Now().After(entry.expiresAt) {
		// Apagar a cópia vencida garante que o próximo pedido busque a versão atual.
		delete(c.entries, key)
		return nil, "", false
	}
	return entry.body, entry.contentType, true
}

// set guarda uma nova cópia e calcula o instante de expiração usando o TTL.
func (c *memoryCache) set(key string, body []byte, contentType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = cacheEntry{
		body:        body,
		contentType: contentType,
		expiresAt:   time.Now().Add(c.ttl),
	}
}

// purge remove uma chave específica; uma chave vazia limpa todo o cache.
// O número devolvido informa quantas cópias foram removidas.
func (c *memoryCache) purge(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if key != "" {
		if _, found := c.entries[key]; found {
			delete(c.entries, key)
			return 1
		}
		return 0
	}

	removed := len(c.entries)
	c.entries = make(map[string]cacheEntry)
	return removed
}

// size devolve o número atual de cópias guardadas.
func (c *memoryCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	return len(c.entries)
}
