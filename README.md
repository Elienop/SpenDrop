# SpenDrop

Self-hosted financial expense tracker for households. Track monthly and yearly expenses with budgets, forecasts, charts, and Excel import/export.

## Tech Stack

- **Backend:** Go
- **Frontend:** React + TypeScript (Vite)
- **Database:** SQLite
- **Deploy:** Docker

## Development

### Prerequisites
- Go 1.23+
- Node.js 20+
- Docker (for production)

### Backend
```bash
go run ./cmd/spendrop
```

### Frontend
```bash
cd web
npm install
npm run dev
```

### Docker
```bash
docker compose up -d
```

## Status

🚧 Project initialization — architecture pending Excel template review.
