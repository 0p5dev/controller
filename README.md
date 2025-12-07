# OpsController API

A REST API for managing Cloud Run deployments and container images with integrated monitoring and metrics.

## Features

- 🔐 JWT-based authentication
- 🚀 Cloud Run deployment management
- 📦 Container registry integration with Google Artifact Registry
- 📊 Real-time metrics and monitoring via Google Cloud Monitoring
- 🗄️ PostgreSQL database with connection pooling
- 📝 Auto-generated OpenAPI/Swagger documentation
- 🔄 Hot-reload development environment

## Tech Stack

- **Language**: Go 1.25.3
- **Framework**: Gin (HTTP web framework)
- **Database**: PostgreSQL with pgx driver
- **Cloud**: Google Cloud Platform (Cloud Run, Artifact Registry, Secret Manager, Monitoring)
- **IaC**: Pulumi for infrastructure deployment
- **Container**: Docker with Docker Compose
- **Dev Tools**: Air (hot reload), Just (command runner), Swag (OpenAPI docs)

## Prerequisites

- [Go 1.25+](https://go.dev/doc/install)
- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/install/)
- [Just](https://github.com/casey/just#installation) - Command runner (like Make but better)
- [Air](https://github.com/cosmtrek/air) - Live reload for Go apps (optional but recommended)
- [Swag](https://github.com/swaggo/swag) - Swagger documentation generator
- GCP Service Account with appropriate permissions (for production)

### Installing Development Tools

```bash
# Install Just (macOS)
brew install just

# Install Just (Linux)
curl --proto '=https' --tlsv1.2 -sSf https://just.systems/install.sh | bash -s -- --to /usr/local/bin

# Install Air
go install github.com/cosmtrek/air@latest

# Install Swag
go install github.com/swaggo/swag/cmd/swag@latest
```

## Quick Start

### 1. Clone and Setup

```bash
git clone https://github.com/digizyne/lfcont.git
cd lfcont

# Install Go dependencies
just tidy
```

### 2. Environment Configuration

Create a `.env` file in the project root:

```env
# Database Configuration
DATABASE_URL=postgresql://postgres:postgres@postgres:5432/postgres

# JWT Secret (generate a secure random string)
JWT_SECRET=your-super-secret-jwt-key-change-this-in-production

# GCP Configuration
GCP_PROJECT_ID=your-gcp-project-id
GCP_REGION=us-central1
GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account-key.json

# Optional: Development Settings
GIN_MODE=debug
```

### 3. Start Development Environment

```bash
# Start with hot-reload (dev profile with Air)
just up

# Or start locally with PostgreSQL
just up-local
```

The API will be available at `http://localhost:8080`

### 4. Generate Swagger Documentation

```bash
just swagger
```

Access the Swagger UI at: `http://localhost:8080/swagger/index.html` (once integrated)

## Available Commands (Justfile)

The project uses [Just](https://github.com/casey/just) as a command runner. Here are the available commands:

```bash
# Development
just                    # Format code and start dev environment (default)
just fmt               # Format Go code
just up                # Start dev environment with Docker (hot-reload)
just up-local          # Start with local PostgreSQL
just down              # Stop and cleanup containers
just down --rmi        # Stop and remove container images
just down-local        # Stop local environment and cleanup

# Build & Run
just build             # Build binary to ~/go/bin/controller
just build-docker      # Build Docker image
just run-docker        # Run Docker container locally

# Dependencies
just tidy              # Clean up go.mod and go.sum
just add <package>     # Add a Go package

# Documentation
just swagger           # Generate OpenAPI/Swagger documentation

# Container Registry (GCP Artifact Registry)
just ar-push <tag>     # Build, tag, and push to Artifact Registry
just tag <tag>         # Tag Docker image
just push <tag>        # Push tagged image to registry
```

## API Endpoints

### Authentication

- `POST /api/v1/auth/register` - Register a new user
- `POST /api/v1/auth/login` - Login and receive JWT token
- `GET /api/v1/auth/supabase-credentials` - Get Supabase credentials

### Deployments

All deployment endpoints require Bearer token authentication.

- `GET /api/v1/deployments` - List all deployments (paginated)
  - Query params: `page`, `limit`, `search`
- `GET /api/v1/deployments/:name` - Get deployment details with metrics
- `POST /api/v1/deployments` - Create or update a deployment

### Container Images

- `POST /api/v1/container-images` - Push container image to registry

### Health

- `GET /health` - Health check endpoint
- `GET /api/v1/health` - API health check with database status

## Project Structure

```
.
├── cmd/
│   └── main.go                      # Application entry point
├── internal/
│   ├── api/                         # HTTP handlers and routes
│   │   ├── auth.go                  # Authentication handlers
│   │   ├── createDeployment.go      # Deployment creation logic
│   │   ├── getDeploymentByName.go   # Deployment details with metrics
│   │   ├── listDeployments.go       # List deployments with pagination
│   │   ├── pushToContainerRegistry.go # Container registry operations
│   │   ├── health.go                # Health check handlers
│   │   └── routes.go                # Route definitions
│   ├── data/                        # Database layer
│   │   ├── database.go              # Database initialization
│   │   └── models/                  # Data models
│   │       ├── user.go
│   │       ├── deployment.go
│   │       └── containerImage.go
│   └── middleware/
│       └── gcpLogger.go             # GCP Cloud Logging integration
├── tools/
│   └── main.go                      # JWT utilities and helpers
├── docs/                            # Auto-generated Swagger docs
├── docker-compose.yml               # Docker Compose configuration
├── Dockerfile                       # Multi-stage Docker build
├── .air.toml                        # Air configuration (hot reload)
├── justfile                         # Command definitions
└── go.mod                           # Go module dependencies
```

## Database Schema

The application automatically creates the following tables on startup:

### `users`
- `username` (TEXT, PRIMARY KEY)
- `password_hash` (TEXT)

### `container_images`
- `fqin` (TEXT, PRIMARY KEY) - Fully Qualified Image Name
- `username` (TEXT)

### `deployments`
- `id` (UUID, PRIMARY KEY)
- `name` (TEXT)
- `url` (TEXT)
- `container_image` (TEXT, FK to container_images)
- `user_email` (TEXT)
- `min_instances` (INT, DEFAULT 0)
- `max_instances` (INT, DEFAULT 1)
- `created_at` (TIMESTAMPTZ)
- `updated_at` (TIMESTAMPTZ)

## Development Workflow

### 1. Making Changes

The development environment uses Air for hot-reload. Simply edit your code and save - the application will automatically rebuild and restart.

```bash
# Start dev environment
just up

# Edit files in your editor
# Changes are automatically detected and applied
```

### 2. Adding Dependencies

```bash
just add github.com/some/package
```

### 3. Testing Endpoints

```bash
# Register a user
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser123","password":"securepassword123"}'

# Login
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser123","password":"securepassword123"}'

# Use the returned JWT token
export TOKEN="your-jwt-token-here"

# List deployments
curl -X GET http://localhost:8080/api/v1/deployments \
  -H "Authorization: Bearer $TOKEN"
```

### 4. Updating API Documentation

After adding or modifying endpoint annotations:

```bash
just swagger
```

The OpenAPI documentation will be regenerated in the `docs/` directory.

## Docker Deployment

### Build and Run Locally

```bash
# Build image
just build-docker

# Run container
just run-docker
```

### Deploy to GCP Artifact Registry

```bash
# Build, tag, and push in one command
just ar-push v1.0.0

# Or do it step by step
just build-docker
just tag v1.0.0
just push v1.0.0
```

## Configuration

### Air Configuration (.air.toml)

The project includes Air for hot-reload during development. Configuration is in `.air.toml`.

### Docker Compose Profiles

- `dev` - Development environment with hot-reload (no local PostgreSQL)
- `local` - Full local environment including PostgreSQL

```bash
# Use dev profile (connect to external DB)
just up

# Use local profile (includes PostgreSQL)
just up-local
```

## Security Notes

- **JWT Secret**: Always use a strong, randomly generated secret in production
- **Database Credentials**: Never commit real credentials to version control
- **GCP Service Account**: Store service account keys securely, never in the repository
- **CORS**: The current configuration allows all origins (`*`). Restrict this in production

## Troubleshooting

### Port Already in Use

```bash
# Check what's using port 8080
lsof -i :8080

# Stop existing containers
just down
```

### Database Connection Issues

```bash
# Check if PostgreSQL is running
docker ps | grep postgres

# Check logs
docker logs lfcont-db

# Verify environment variables
cat .env
```

### Hot Reload Not Working

```bash
# Ensure Air is installed
which air

# Check Air logs in the container
docker logs lfcont
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

TODO: determine which license to use

## Support

For issues and questions, please open an issue on GitHub.
