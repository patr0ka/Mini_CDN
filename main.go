package main

import (
	"flag"
	"log"
	"strings"
	"time"
)

func main() {
	mode := flag.String("mode", "", "papel do processo: origin, edge ou gateway")
	addr := flag.String("addr", "", "endereço local para escutar, por exemplo :8080")
	originURL := flag.String("origin", "http://localhost:8080", "endereço do servidor de origem")
	name := flag.String("name", "edge", "nome deste cache")
	ttl := flag.Duration("ttl", 30*time.Second, "tempo que uma cópia fica válida no cache")
	edgesCSV := flag.String("edges", "", "endereços dos caches separados por vírgula")
	healthInterval := flag.Duration("health-interval", 2*time.Second, "intervalo entre verificações dos caches")

	flag.Parse()

	switch *mode {
	case "origin":
		// O servidor de origem guarda o conteúdo oficial.
		if *addr == "" {
			*addr = ":8080"
		}
		runOrigin(*addr)
	case "edge":
		// Um edge é um cache que busca conteúdo na origem quando ainda não o tem.
		if *addr == "" {
			log.Fatal("informe -addr para iniciar um cache, por exemplo -addr=:8081")
		}
		runEdge(*addr, *originURL, *name, *ttl)
	case "gateway":
		// O gateway recebe pedidos dos clientes e escolhe um cache disponível.
		if *addr == "" {
			*addr = ":8000"
		}
		if strings.TrimSpace(*edgesCSV) == "" {
			log.Fatal("informe -edges com os endereços dos caches")
		}
		addresses := strings.Split(*edgesCSV, ",")
		runGateway(*addr, *originURL, addresses, *healthInterval)
	default:
		// Um erro claro ajuda quando o programa é iniciado sem o papel correto.
		log.Fatal("use -mode=origin, -mode=edge ou -mode=gateway")
	}
}
