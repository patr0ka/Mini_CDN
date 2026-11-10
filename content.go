package main

import (
	// encoding/json converte structs Go para JSON e JSON recebido para structs.
	"encoding/json"
	// fmt formata mensagens e endereços.
	"fmt"
	// log encerra o processo com uma mensagem se o servidor não puder iniciar.
	"log"
	// net/http implementa o servidor web e as respostas HTTP.
	"net/http"
	// strings ajuda a extrair o identificador do conteúdo do caminho HTTP.
	"strings"
	// sync fornece o mutex usado para proteger o mapa compartilhado.
	"sync"
	// time registra quando o conteúdo foi atualizado.
	"time"
)

// Content descreve um item que o servidor de origem disponibiliza.
// As tags json dizem os nomes dos campos quando o Go transforma o struct em JSON.
type Content struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// contentUpdate é o formato JSON esperado ao alterar um conteúdo.
type contentUpdate struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// contentStore mantém os conteúdos da origem em memória.
// O mutex permite que várias requisições acessem o mapa sem corrompê-lo.
type contentStore struct {
	mu    sync.RWMutex
	items map[string]Content
}

// newContentStore cria a origem com alguns conteúdos para a demonstração.
func newContentStore() *contentStore {
	now := time.Now()
	return &contentStore{
		items: map[string]Content{
			"boas-vindas": {
				ID: "boas-vindas", Title: "Bem-vindo à mini CDN",
				Body: "Este conteúdo veio do servidor de origem.", Version: 1, UpdatedAt: now,
			},
			"video": {
				ID: "video", Title: "Vídeo da aula",
				Body: "Conteúdo de exemplo para ser armazenado nos caches.", Version: 1, UpdatedAt: now,
			},
			"aula-sd": {
				ID: "aula-sd", Title: "Sistemas distribuídos",
				Body: "Origem, caches, balanceamento e invalidação.", Version: 1, UpdatedAt: now,
			},
		},
	}
}

// get devolve um item sem permitir que o chamador altere o mapa diretamente.
func (s *contentStore) get(key string) (Content, bool) {
	// RLock permite leituras simultâneas; Unlock libera o bloqueio ao sair.
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, found := s.items[key]
	return item, found
}

// update cria ou substitui um item e aumenta sua versão.
func (s *contentStore) update(key string, update contentUpdate) Content {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous, found := s.items[key]
	version := 1
	if found {
		version = previous.Version + 1
	}

	item := Content{
		ID: key, Title: update.Title, Body: update.Body,
		Version: version, UpdatedAt: time.Now(),
	}
	s.items[key] = item
	return item
}

// runOrigin inicia o servidor que guarda a versão oficial dos conteúdos.
func runOrigin(addr string) {
	store := newContentStore()
	mux := http.NewServeMux()

	// /health é usado pelo balanceador para saber se a origem está funcionando.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "origin"})
	})

	// GET /content/{id} devolve um conteúdo que já existe na origem.
	mux.HandleFunc("/content/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		key, ok := keyFromPath(r.URL.Path, "/content/")
		if !ok {
			writeError(w, http.StatusBadRequest, "identificador inválido")
			return
		}

		item, found := store.get(key)
		if !found {
			writeError(w, http.StatusNotFound, "conteúdo não encontrado")
			return
		}
		logRequest("ORIGIN", "entregou %s (versão %d)", key, item.Version)
		writeJSON(w, http.StatusOK, item)
	})

	// POST /admin/content/{id} cria ou atualiza um conteúdo na origem.
	mux.HandleFunc("/admin/content/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		key, ok := keyFromPath(r.URL.Path, "/admin/content/")
		if !ok {
			writeError(w, http.StatusBadRequest, "identificador inválido")
			return
		}

		var update contentUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			writeError(w, http.StatusBadRequest, "JSON inválido: informe title e body")
			return
		}
		if strings.TrimSpace(update.Title) == "" {
			writeError(w, http.StatusBadRequest, "title não pode ficar vazio")
			return
		}

		item := store.update(key, update)
		logRequest("ORIGIN", "atualizou %s para a versão %d", key, item.Version)
		writeJSON(w, http.StatusOK, item)
	})

	logRequest("ORIGIN", "servidor de origem ouvindo em http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// keyFromPath extrai e valida um identificador simples usado como caminho.
// Permitimos letras, números, hífen e sublinhado para evitar caminhos inesperados.
func keyFromPath(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	key := strings.TrimPrefix(path, prefix)
	if key == "" {
		return "", false
	}
	for _, character := range key {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return "", false
		}
	}
	return key, true
}

// writeJSON escreve uma resposta HTTP como JSON.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Neste ponto os cabeçalhos já foram enviados; o log ainda ajuda a depurar.
		logRequest("HTTP", "erro ao codificar JSON: %v", err)
	}
}

// writeError padroniza respostas de erro para todos os serviços.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// logRequest adiciona data e hora às mensagens de cada serviço.
func logRequest(service, format string, values ...any) {
	message := fmt.Sprintf(format, values...)
	log.Printf("[%s] %s", service, message)
}
