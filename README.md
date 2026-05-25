# viralefy_api

API Go que orquestra planos de seguidores, checkout com cadastro, pedidos e gateways de pagamento.

## Diretrizes

Siga [../viralefy_archive/diretrizes.md](../viralefy_archive/diretrizes.md) e [../AGENTS.md](../AGENTS.md).

## Rodar

```bash
# Na raiz do monorepo
docker compose up -d

cd viralefy_api
export DATABASE_URL=postgres://viralefy:viralefy@localhost:5432/viralefy?sslmode=disable
make dev
```

## Endpoints principais

| Método | Rota | Auth |
|--------|------|------|
| GET | `/v1/plans` | Público |
| POST | `/v1/checkout` | Público |
| POST | `/v1/auth/login` | Público |
| CRUD | `/v1/admin/plans` | Bearer admin |
| CRUD | `/v1/admin/gateways` | Bearer admin |
| GET | `/v1/admin/orders` | Bearer admin |

Admin seed: `admin@viralefy.local` / `SimTest!Admin2026`

## OpenAPI

Ver [docs/openapi.yaml](docs/openapi.yaml).
