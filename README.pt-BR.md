# cloudflared-controller

[![Container Build](https://github.com/ronaldmiranda/docker-cloudflared-controller/actions/workflows/container_build.yml/badge.svg)](https://github.com/ronaldmiranda/docker-cloudflared-controller/actions/workflows/container_build.yml)
[![Docker Hub](https://img.shields.io/docker/v/ronaldmiranda/cloudflared-controller?label=docker%20hub)](https://hub.docker.com/r/ronaldmiranda/cloudflared-controller)

*Read this in [English](README.md).*

Controller em Go que monitora containers Docker e gerencia automaticamente as
**application routes (public hostnames)** de um túnel Cloudflare existente.

Adicione duas labels a qualquer serviço do seu `docker-compose` e ele fica
acessível publicamente via Cloudflare Tunnel — sem abrir portas, sem editar
config do túnel na mão:

```yaml
services:
  jellyfin:
    image: jellyfin/jellyfin
    labels:
      cloudflared.hostname: media.exemplo.com
      cloudflared.port: "8096"
```

Quando o container sobe, o controller:

1. Adiciona uma ingress rule `media.exemplo.com -> http://jellyfin:8096` na
   configuração remota do túnel;
2. Cria o CNAME proxied `media.exemplo.com -> <tunnel-id>.cfargotunnel.com`.

Quando o container para ou é removido, a rota e o DNS são removidos.

## Como funciona

- Escuta eventos do daemon Docker (`start`, `stop`, `die`, ...) com debounce,
  além de um resync periódico (padrão: 5 min).
- Cada reconcile lista os containers em execução, monta a lista de ingress
  rules desejada e faz `PUT` na configuração remota do túnel
  (`/accounts/{account}/cfd_tunnel/{tunnel}/configurations`) apenas se houver
  diferença. Uma regra catch-all `http_status:404` é sempre mantida no final.
- DNS: cria/atualiza CNAMEs com o comentário `managed by
  cloudflared-controller` e só remove registros que possuem esse comentário —
  registros manuais nunca são tocados. Só as zonas referenciadas pelos
  hostnames das labels são acessadas, então o token pode ter `Zone > DNS >
  Edit` restrito a essas zonas. Falhas de DNS são logadas sem derrubar o
  controller (as rotas do túnel continuam sendo geridas).

> **Importante:** o controller assume a posse da lista de ingress rules do
> túnel. Use um túnel dedicado ao host Docker; rotas adicionadas manualmente
> nesse túnel serão sobrescritas no próximo reconcile.

## Labels

| Label | Obrigatória | Descrição |
|---|---|---|
| `cloudflared.hostname` | sim | Hostname público (ex.: `app.exemplo.com`) |
| `cloudflared.port` | sim* | Porta do serviço dentro do container |
| `cloudflared.service` | não | URL de origem completa (ex.: `https://10.0.0.5:8443`); substitui scheme/target/port |
| `cloudflared.scheme` | não | `http` (padrão) ou `https` |
| `cloudflared.target` | não | Host de origem; padrão é o nome do container |
| `cloudflared.network` | não | Rede Docker; usa o IP do container nessa rede como origem, em vez do nome |
| `cloudflared.path` | não | Regex de path para roteamento por caminho |
| `cloudflared.notlsverify` | não | `true` para pular verificação TLS da origem |

\* dispensada se `cloudflared.service` for informada.

O `cloudflared` alcança a origem pelo nome do container, então **o container
do cloudflared e os serviços expostos precisam compartilhar uma rede Docker**
(veja o exemplo). Para expor algo fora dessa rede, use `cloudflared.target`
ou `cloudflared.service` apontando para um IP/host alcançável.

Quando o container está em **outro compose** e o nome não resolve pelo DNS do
Docker, use `cloudflared.network` com o nome da rede compartilhada — o
controller passa a usar o IP do container nessa rede (e o atualiza a cada
restart do container):

```yaml
labels:
  cloudflared.hostname: app.exemplo.com
  cloudflared.port: "8080"
  cloudflared.network: proxy
```

`cloudflared.network` e `cloudflared.target` são mutuamente exclusivas.

### Múltiplas rotas no mesmo container

Use um índice no meio da label:

```yaml
labels:
  cloudflared.0.hostname: web.exemplo.com
  cloudflared.0.port: "80"
  cloudflared.1.hostname: api.exemplo.com
  cloudflared.1.port: "3000"
```

## Configuração (variáveis de ambiente)

| Variável | Obrigatória | Padrão | Descrição |
|---|---|---|---|
| `CLOUDFLARE_API_TOKEN` | sim | — | Token com `Account > Cloudflare Tunnel > Edit` e `Zone > DNS > Edit` |
| `CLOUDFLARE_ACCOUNT_ID` | sim | — | ID da conta Cloudflare |
| `TUNNEL_ID` | sim | — | UUID do túnel (remotely-managed) |
| `LABEL_PREFIX` | não | `cloudflared` | Namespace das labels |
| `RESYNC_INTERVAL` | não | `5m` | Intervalo do reconcile periódico |
| `MANAGE_DNS` | não | `true` | `false` desativa a gestão de CNAMEs |
| `LOG_LEVEL` | não | `info` | `debug` para logs detalhados |
| `DOCKER_HOST` | não | socket local | Endereço do daemon Docker |

## Rodando

Imagens para `linux/amd64` e `linux/arm64` são publicadas no
[Docker Hub](https://hub.docker.com/r/ronaldmiranda/cloudflared-controller).
Veja `docker-compose.yml` para a stack completa (cloudflared + controller +
app de exemplo).

Resumo:

Exemplo de `.env`:

```env
  CLOUDFLARE_API_TOKEN=...
  CLOUDFLARE_ACCOUNT_ID=...
  TUNNEL_ID=...
  DOCKER_GID=999 # stat -c %g /var/run/docker.sock
```

Exemplo de `docker-compose.yml`:

```yaml
services:
  cloudflared-controller:
    image: ronaldmiranda/cloudflared-controller:latest
    environment:
      CLOUDFLARE_API_TOKEN: ${CLOUDFLARE_API_TOKEN}
      CLOUDFLARE_ACCOUNT_ID: ${CLOUDFLARE_ACCOUNT_ID}
      TUNNEL_ID: ${TUNNEL_ID}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    group_add:
      - "${DOCKER_GID:-999}" # GID do grupo docker: stat -c %g /var/run/docker.sock
    restart: unless-stopped
```

A imagem roda como usuário não-root, então o container precisa entrar no
grupo dono do socket (`group_add`) para ler `/var/run/docker.sock`. Sem isso
o controller sai com `permission denied ... docker.sock`.

Ou direto no host:

```sh
go build ./cmd/cloudflared-controller
CLOUDFLARE_API_TOKEN=... CLOUDFLARE_ACCOUNT_ID=... TUNNEL_ID=... ./cloudflared-controller
```

## Desenvolvimento

```sh
go test ./...
go vet ./...
```

Estrutura:

```
cmd/cloudflared-controller/   entrypoint
internal/config/              carga de configuração via env
internal/docker/              descoberta de rotas por labels + watch de eventos
internal/cloudflare/          cliente mínimo da API v4 (tunnel config + DNS)
internal/controller/          loop de reconciliação (ingress + DNS)
```

### Pipeline de release

O workflow `.github/workflows/container_build.yml` valida `gofmt`, `go vet` e
`go test -race`, depois builda a imagem multiarch (`linux/amd64` +
`linux/arm64`, via cross-compile — sem QEMU) e publica no Docker Hub:

- push na `main` → tags `latest` e `sha-<commit>`
- tag `vX.Y.Z` → tags `X.Y.Z` e `X.Y`

Requer a variável `DOCKERHUB_USERNAME` e o secret `DOCKERHUB_TOKEN` no
repositório (access token do Docker Hub com *Read & Write*).
