# LiteDNS

LiteDNS project scaffold initialized with:
- Backend: Go + Gin
- Frontend: Vue 3 + Vite + Tailwind CSS
- Database: SQLite

## Directory Layout

- `cmd/litedns`: backend entrypoint
- `internal`: backend core modules
- `frontend`: web console source
- `configs`: example configuration
- `scripts`: local dev/build scripts
- `build/docker`: container build files
- `docs`: architecture and design documents

## Dev Startup

- `./scripts/dev.sh`
  - 同时启动后端服务（Go）和前端开发服务（Vite）。
- `./scripts/dev.sh --master-key "<BASE64_32_BYTE_KEY>"`
  - 指定自定义 `LITEDNS_MASTER_KEY`，并同时启动前后端。
