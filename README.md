# Mini CDN didática em Go

Este projeto demonstra uma pequena Rede de Distribuição de Conteúdo (CDN). Há um servidor de origem, três caches independentes e um balanceador. Todos são processos separados e conversam por HTTP, mesmo quando rodam no mesmo computador.

## O que o projeto demonstra

- **Cache e TTL:** cada cache guarda uma cópia por um tempo configurável. A primeira consulta é `MISS`; as consultas seguintes podem ser `HIT`.
- **Balanceamento round-robin:** o gateway distribui os pedidos em rodízio entre caches saudáveis.
- **Verificação de saúde:** o gateway consulta `/health` dos caches e evita enviar pedidos a um cache que parou.
- **Invalidação:** uma atualização feita pelo gateway altera a origem e pede aos caches que apaguem a cópia antiga.
- **Métricas:** `/stats` mostra pedidos, `HIT`s, `MISS`es, consultas à origem, falhas e latência média.

## Requisitos

- Go instalado (versão 1.22 ou mais recente).
- Windows PowerShell para usar o script de inicialização incluído.
- O projeto usa somente a biblioteca padrão do Go; não precisa baixar bibliotecas adicionais.

## Iniciar todos os serviços no Windows

Na pasta do projeto, execute:

```powershell
.\start-demo.ps1
```

O script abre cinco janelas de PowerShell com os logs da origem, dos três caches e do gateway. Os caches usam TTL de 20 segundos para facilitar a demonstração. Para encerrar cada serviço, pressione `Ctrl+C` na janela correspondente.

Se o PowerShell bloquear a execução do script, rode-o nesta sessão com:

```powershell
powershell -ExecutionPolicy Bypass -File .\start-demo.ps1
```

Depois de instalar Go, abra um terminal novo para que o comando `go` apareça no `PATH`. O script de inicialização também procura Go no diretório padrão de instalação do Windows.

## Iniciar manualmente

Se preferir, abra cinco terminais na pasta do projeto e execute um comando em cada terminal:

```powershell
go run . -mode=origin -addr=:8080
```

```powershell
go run . -mode=edge -name=edge-a -addr=:8081 -origin=http://localhost:8080 -ttl=30s
```

```powershell
go run . -mode=edge -name=edge-b -addr=:8082 -origin=http://localhost:8080 -ttl=30s
```

```powershell
go run . -mode=edge -name=edge-c -addr=:8083 -origin=http://localhost:8080 -ttl=30s
```

```powershell
go run . -mode=gateway -addr=:8000 -origin=http://localhost:8080 -edges=http://localhost:8081,http://localhost:8082,http://localhost:8083 -health-interval=2s
```

## Roteiro para a apresentação

### 1. Mostrar balanceamento e cache

Faça quatro pedidos ao mesmo conteúdo:

```powershell
curl.exe -i http://localhost:8000/content/boas-vindas
```

O gateway envia os três primeiros pedidos, um por vez, a caches diferentes. Cada cache começa vazio, então os três mostram `X-Cache: MISS` e buscam o conteúdo na origem. O quarto pedido volta ao primeiro cache e deve mostrar `X-Cache: HIT`. A resposta também inclui o cabeçalho `X-Gateway-Edge`, que identifica o cache escolhido.

Os terminais deixam visível o caminho do pedido: `GATEWAY`, depois o `edge` selecionado e, nos `MISS`, o `ORIGIN`.

### 2. Mostrar as métricas

Abra no navegador ou consulte pelo terminal:

```powershell
curl.exe http://localhost:8000/stats
```

O resultado reúne contadores de cada cache, incluindo quantos pedidos foram `HIT` ou `MISS` e quantas cópias ele guarda.

### 3. Atualizar e invalidar uma cópia

Envie uma atualização pelo gateway. No PowerShell:

```powershell
$dados = @{ title = "Título atualizado"; body = "Esta é a nova versão do conteúdo." } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri "http://localhost:8000/admin/update/boas-vindas" -ContentType "application/json" -Body $dados
```

A resposta informa a nova versão e quais caches foram invalidados. Ao pedir o conteúdo novamente, os caches precisam buscar a versão nova na origem.

### 4. Simular a falha de um cache

Feche a janela do `edge-b` com `Ctrl+C`. Depois de até dois segundos, o gateway deve registrar que ele está indisponível e continuar encaminhando pedidos aos caches restantes. Consulte `/stats` para ver o estado de saúde.

### 5. Demonstrar o TTL

Com a configuração do script, espere mais de 20 segundos desde o último `MISS` de um cache e peça o conteúdo novamente. A cópia venceu e o cache busca uma nova versão na origem. No início de uma apresentação, TTLs menores, como `10s`, deixam essa etapa mais rápida.

## Endpoints

| Serviço | Rota | Uso |
|---|---|---|
| Origem | `GET /content/{id}` | Lê um conteúdo da origem |
| Origem | `POST /admin/content/{id}` | Atualiza um conteúdo diretamente na origem |
| Cache | `GET /content/{id}` | Busca no cache ou consulta a origem |
| Cache | `POST /admin/purge?key={id}` | Invalida uma cópia; sem `key`, limpa o cache |
| Cache | `GET /stats` | Mostra métricas do cache |
| Gateway | `GET /content/{id}` | Distribui o pedido entre os caches saudáveis |
| Gateway | `POST /admin/update/{id}` | Atualiza a origem e invalida as cópias |
| Gateway | `GET /stats` | Mostra saúde e métricas dos caches |
| Todos | `GET /health` | Verifica se o serviço está ativo |

Os identificadores de conteúdo aceitam letras, números, `-` e `_`. Os endpoints administrativos são abertos de propósito para a demonstração em máquina local; não exponha este protótipo diretamente à internet.

## Como o código está organizado

- `main.go`: lê as opções `-mode`, `-addr` e outras, e inicia um papel do sistema.
- `content.go`: define os conteúdos e implementa a origem.
- `cache.go`: guarda cópias em memória e aplica o TTL.
- `edge.go`: implementa cada cache, o comportamento `HIT`/`MISS` e suas métricas.
- `gateway.go`: implementa o balanceador, a verificação de saúde, as estatísticas agregadas e a invalidação.
- `start-demo.ps1`: abre uma janela para cada serviço.
- `comandos.txt`: reúne os comandos para iniciar, consultar, medir e atualizar conteúdos durante a demonstração.

Os comentários no código explicam os elementos de Go usados, como funções, structs, ponteiros, mutexes, handlers HTTP, goroutines e `defer`.

## Go em poucas palavras

- `package main` indica que os arquivos formam um programa executável.
- `import (...)` lista os pacotes da biblioteca padrão que o arquivo usa.
- `func nome(...)` declara uma função. `main()` é a primeira função executada.
- `type Nome struct { ... }` cria um tipo que agrupa campos, parecido com um registro.
- `:=` declara uma variável e deixa o Go deduzir seu tipo, por exemplo `name := "edge-a"`.
- `*Tipo` representa um ponteiro para um valor. `&valor` obtém o endereço desse valor.
- `func (g *gateway) ...` declara um método ligado ao tipo `gateway`; `g` é o valor que o método pode consultar ou alterar.
- `defer` agenda uma ação para ocorrer quando a função terminar; aqui ele libera mutexes e fecha respostas HTTP.
- `go algumaFuncao()` inicia uma goroutine, uma tarefa concorrente leve. O gateway usa uma para verificar os caches enquanto atende pedidos.
- Um `sync.Mutex` protege dados compartilhados: apenas uma goroutine por vez entra na parte do código entre `Lock` e `Unlock`.
- Um handler HTTP é uma função chamada quando chega um pedido para uma rota, como `/content/boas-vindas`.

O mesmo código é executado com `-mode=origin`, `-mode=edge` ou `-mode=gateway`; essa opção escolhe o papel daquele processo.
