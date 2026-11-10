	package main

import (
	// io lê a resposta devolvida pelo servidor de origem.
	"io"
	// log encerra o processo se não for possível iniciar o servidor.
	"log"
	// net/http contém cliente e servidor HTTP.
	"net/http"
	// strings remove a barra final do endereço da origem.
	"strings"
	// sync protege os contadores de métricas.
	"sync"
	// time mede a duração das requisições.
	"time"
)

// edgeMetrics guarda contadores deste cache específico.
type edgeMetrics struct {
	mu             sync.Mutex
	requests       int64
	hits           int64
	misses         int64
	originFetches  int64
	originErrors   int64
	totalLatencyMS float64
}

// edgeStats é a versão segura para enviar ao cliente como JSON.
type edgeStats struct {
	Requests       int64   `json:"requests"`
	Hits           int64   `json:"hits"`
	Misses         int64   `json:"misses"`
	OriginFetches  int64   `json:"origin_fetches"`
	OriginErrors   int64   `json:"origin_errors"`
	AverageLatency float64 `json:"average_latency_ms"`
	CacheEntries   int     `json:"cache_entries"`
}

// recordRequest atualiza a quantidade de pedidos e sua duração total.
func (m *edgeMetrics) recordRequest(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	m.totalLatencyMS += float64(duration) / float64(time.Millisecond)
}

// addHit conta uma resposta servida diretamente do cache.
func (m *edgeMetrics) addHit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hits++
}

// addMiss conta um pedido que não encontrou uma cópia válida no cache.
func (m *edgeMetrics) addMiss() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.misses++
}

// addOriginFetch conta uma consulta feita à origem.
func (m *edgeMetrics) addOriginFetch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.originFetches++
}

// addOriginError conta uma falha ao conversar com a origem.
func (m *edgeMetrics) addOriginError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.originErrors++
}

// snapshot copia os contadores para que possam ser serializados como JSON.
func (m *edgeMetrics) snapshot(cacheEntries int) edgeStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	average := float64(0)
	if m.requests > 0 {
		average = m.totalLatencyMS / float64(m.requests)
	}
	return edgeStats{
		Requests: m.requests, Hits: m.hits, Misses: m.misses,
		OriginFetches: m.originFetches, OriginErrors: m.originErrors,
		AverageLatency: average, CacheEntries: cacheEntries,
	}
}

// runEdge inicia um cache de borda, que atende clientes e consulta a origem em caso de MISS.
func runEdge(addr, originURL, name string, ttl time.Duration) {
	originURL = strings.TrimRight(originURL, "/")
	cache := newMemoryCache(ttl)
	metrics := &edgeMetrics{}
	client := &http.Client{Timeout: 5 * time.Second}
	mux := http.NewServeMux()

	// /health responde rapidamente ao balanceador, sem acessar a origem.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "edge": name})
	})

	// /stats mostra contadores deste edge e a quantidade de cópias em memória.
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		writeJSON(w, http.StatusOK, metrics.snapshot(cache.size()))
	})

	// POST /admin/purge?key=video invalida uma cópia; sem key, limpa o cache todo.
	mux.HandleFunc("/admin/purge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		key := r.URL.Query().Get("key")
		if key != "" {
			if _, ok := keyFromPath("/content/"+key, "/content/"); !ok {
				writeError(w, http.StatusBadRequest, "identificador inválido")
				return
			}
		}
		removed := cache.purge(key)
		logRequest(name, "invalidação removeu %d cópia(s) de %q", removed, key)
		writeJSON(w, http.StatusOK, map[string]any{"edge": name, "removed": removed, "key": key})
	})

	// /content/{id} é o caminho usado pelo balanceador para pedir conteúdo ao cache.
	mux.HandleFunc("/content/", func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		defer func() { metrics.recordRequest(time.Since(started)) }()

		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		key, ok := keyFromPath(r.URL.Path, "/content/")
		if !ok {
			writeError(w, http.StatusBadRequest, "identificador inválido")
			return
		}

		// Uma cópia válida é respondida sem envolver o servidor de origem.
		if body, contentType, found := cache.get(key); found {
			metrics.addHit()
			logRequest(name, "HIT %s", key)
			w.Header().Set("X-Cache", "HIT")
			w.Header().Set("X-Edge", name)
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		}

		// MISS significa que a cópia não existe ou seu TTL já venceu.
		metrics.addMiss()
		metrics.addOriginFetch()
		logRequest(name, "MISS %s; buscando na origem", key)
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, originURL+"/content/"+key, nil)
		if err != nil {
			metrics.addOriginError()
			writeError(w, http.StatusBadGateway, "não foi possível montar o pedido à origem")
			return
		}

		response, err := client.Do(request)
		if err != nil {
			metrics.addOriginError()
			writeError(w, http.StatusBadGateway, "servidor de origem indisponível")
			return
		}
		defer response.Body.Close()

		body, err := io.ReadAll(response.Body)
		if err != nil {
			metrics.addOriginError()
			writeError(w, http.StatusBadGateway, "erro ao ler a resposta da origem")
			return
		}

		contentType := response.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json; charset=utf-8"
		}
		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Edge", name)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)

		// Só guardamos respostas bem-sucedidas; erros, como 404, não ficam em cache.
		if response.StatusCode == http.StatusOK {
			cache.set(key, body, contentType)
		}
	})

	logRequest(name, "cache ouvindo em http://localhost%s; origem=%s; TTL=%s", addr, originURL, ttl)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
