package main

import (
	// encoding/json decodifica a atualização e prepara o painel de estatísticas.
	"encoding/json"
	// io copia corpos de pedidos e respostas entre serviços.
	"io"
	// log encerra o processo se o balanceador não puder iniciar.
	"log"
	// net/http oferece os clientes usados para encaminhar pedidos.
	"net/http"
	// strings normaliza os endereços configurados na linha de comando.
	"strings"
	// sync protege o estado compartilhado dos caches e do rodízio.
	"sync"
	// time controla a frequência das verificações de saúde.
	"time"
)

// edgeNode representa um dos caches conhecidos pelo balanceador.
type edgeNode struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
}

// gateway contém o estado que o balanceador precisa compartilhar entre pedidos.
type gateway struct {
	originURL string
	edges     []edgeNode
	mu        sync.Mutex
	next      int
	client    *http.Client
}

// runGateway inicia o balanceador e cria uma lista de caches a partir das flags.
func runGateway(addr, originURL string, addresses []string, healthInterval time.Duration) {
	edges := make([]edgeNode, 0, len(addresses))
	for _, address := range addresses {
		address = strings.TrimRight(strings.TrimSpace(address), "/")
		if address != "" {
			edges = append(edges, edgeNode{URL: address})
		}
	}
	if len(edges) == 0 {
		logRequest("GATEWAY", "nenhum endereço de cache foi informado")
		return
	}
	if healthInterval <= 0 {
		healthInterval = 2 * time.Second
	}

	g := &gateway{
		originURL: strings.TrimRight(originURL, "/"),
		edges:     edges,
		client:    &http.Client{Timeout: 4 * time.Second},
	}

	// A primeira verificação é feita antes de aceitar pedidos dos clientes.
	g.checkEdges()
	// A goroutine roda esta verificação em paralelo ao servidor HTTP.
	go g.monitorEdges(healthInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gateway"})
	})
	mux.HandleFunc("/stats", g.handleStats)
	mux.HandleFunc("/admin/update/", g.handleUpdate)
	mux.HandleFunc("/content/", g.handleContent)

	logRequest("GATEWAY", "balanceador ouvindo em http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// monitorEdges atualiza a saúde dos caches a cada intervalo configurado.
func (g *gateway) monitorEdges(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		g.checkEdges()
	}
}

// checkEdges chama /health de cada cache e registra quais estão disponíveis.
func (g *gateway) checkEdges() {
	for index, edge := range g.edgeSnapshot() {
		response, err := g.client.Get(edge.URL + "/health")
		healthy := err == nil && response.StatusCode == http.StatusOK
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		g.setHealth(index, healthy)
		if healthy != edge.Healthy {
			logRequest("GATEWAY", "%s agora está %s", edge.URL, healthWord(healthy))
		}
	}
}

// healthWord converte um booleano em uma palavra para as mensagens do terminal.
func healthWord(healthy bool) string {
	if healthy {
		return "saudável"
	}
	return "indisponível"
}

// edgeSnapshot devolve uma cópia da lista para que possa ser percorrida sem travar o gateway.
func (g *gateway) edgeSnapshot() []edgeNode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]edgeNode(nil), g.edges...)
}

// setHealth atualiza o estado de um cache dentro da lista compartilhada.
func (g *gateway) setHealth(index int, healthy bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index >= 0 && index < len(g.edges) {
		g.edges[index].Healthy = healthy
	}
}

// markUnhealthy reage rapidamente se um cache falhar no meio de um pedido.
func (g *gateway) markUnhealthy(address string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for index := range g.edges {
		if g.edges[index].URL == address {
			g.edges[index].Healthy = false
			return
		}
	}
}

// chooseEdge seleciona um cache saudável usando round-robin (rodízio).
func (g *gateway) chooseEdge() (edgeNode, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for checked := 0; checked < len(g.edges); checked++ {
		index := (g.next + checked) % len(g.edges)
		if g.edges[index].Healthy {
			// O próximo pedido começa no cache seguinte da lista.
			g.next = (index + 1) % len(g.edges)
			return g.edges[index], true
		}
	}
	return edgeNode{}, false
}

// handleContent recebe o pedido do cliente e o encaminha ao próximo cache saudável.
func (g *gateway) handleContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	key, ok := keyFromPath(r.URL.Path, "/content/")
	if !ok {
		writeError(w, http.StatusBadRequest, "identificador inválido")
		return
	}

	edge, found := g.chooseEdge()
	if !found {
		writeError(w, http.StatusServiceUnavailable, "nenhum cache está disponível")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, edge.URL+"/content/"+key, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "não foi possível montar o pedido ao cache")
		return
	}
	response, err := g.client.Do(request)
	if err != nil {
		g.markUnhealthy(edge.URL)
		writeError(w, http.StatusBadGateway, "o cache selecionado não respondeu; tente novamente")
		return
	}
	defer response.Body.Close()

	copyHeaders(w, response.Header)
	w.Header().Set("X-Gateway-Edge", edge.URL)
	w.WriteHeader(response.StatusCode)
	if _, err := io.Copy(w, response.Body); err != nil {
		logRequest("GATEWAY", "erro ao enviar resposta de %s: %v", edge.URL, err)
	}
}

// handleUpdate atualiza a origem e depois invalida a cópia antiga em cada cache.
func (g *gateway) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	key, ok := keyFromPath(r.URL.Path, "/admin/update/")
	if !ok {
		writeError(w, http.StatusBadRequest, "identificador inválido")
		return
	}

	// O gateway encaminha o JSON recebido para a rota administrativa da origem.
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.originURL+"/admin/content/"+key, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "não foi possível montar a atualização")
		return
	}
	request.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	response, err := g.client.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "servidor de origem indisponível")
		return
	}
	defer response.Body.Close()

	originBody, err := io.ReadAll(response.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "erro ao ler a resposta da origem")
		return
	}
	if response.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(originBody)
		return
	}

	var updated Content
	if err := json.Unmarshal(originBody, &updated); err != nil {
		writeError(w, http.StatusBadGateway, "a origem devolveu uma resposta inválida")
		return
	}

	// A origem já foi atualizada. Agora pedimos que cada edge apague sua cópia antiga.
	purged := make([]string, 0)
	purgeErrors := make(map[string]string)
	for _, edge := range g.edgeSnapshot() {
		purgeRequest, requestErr := http.NewRequestWithContext(
			r.Context(), http.MethodPost, edge.URL+"/admin/purge?key="+key, nil,
		)
		if requestErr != nil {
			purgeErrors[edge.URL] = requestErr.Error()
			continue
		}
		purgeResponse, purgeErr := g.client.Do(purgeRequest)
		if purgeErr != nil {
			purgeErrors[edge.URL] = purgeErr.Error()
			g.markUnhealthy(edge.URL)
			continue
		}
		_, _ = io.Copy(io.Discard, purgeResponse.Body)
		_ = purgeResponse.Body.Close()
		if purgeResponse.StatusCode != http.StatusOK {
			purgeErrors[edge.URL] = "cache respondeu com status " + purgeResponse.Status
			continue
		}
		purged = append(purged, edge.URL)
	}

	logRequest("GATEWAY", "conteúdo %s atualizado para v%d; caches invalidados=%d; falhas=%d",
		key, updated.Version, len(purged), len(purgeErrors))
	writeJSON(w, http.StatusOK, map[string]any{
		"updated": updated, "purged_edges": purged, "purge_errors": purgeErrors,
	})
}

// gatewayEdgeStats reúne estado de saúde e métricas de cada cache.
type gatewayEdgeStats struct {
	URL     string          `json:"url"`
	Healthy bool            `json:"healthy"`
	Stats   json.RawMessage `json:"stats,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// handleStats consulta /stats em cada cache e devolve uma visão agregada.
func (g *gateway) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	results := make([]gatewayEdgeStats, 0)
	for _, edge := range g.edgeSnapshot() {
		result := gatewayEdgeStats{URL: edge.URL, Healthy: edge.Healthy}
		response, err := g.client.Get(edge.URL + "/stats")
		if err != nil {
			result.Error = "não foi possível consultar as estatísticas"
		} else {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK {
				result.Error = "cache não devolveu as estatísticas"
			} else {
				result.Stats = json.RawMessage(body)
			}
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": results})
}

// copyHeaders copia cabeçalhos da resposta do cache para a resposta ao cliente.
func copyHeaders(destination http.ResponseWriter, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Header().Add(name, value)
		}
	}
}
