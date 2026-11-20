package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fleetdeck/internal/domain"
)

// GitBuilder handles cloning and building applications from Git repositories
type GitBuilder struct {
	workDir string
}

func NewGitBuilder(workDir string) *GitBuilder {
	_ = os.MkdirAll(workDir, 0755)
	return &GitBuilder{workDir: workDir}
}

// BuildFromGit clones repository and builds a Docker image
func (b *GitBuilder) BuildFromGit(ctx context.Context, app *domain.Application) (string, error) {
	if app.GitRepo == "" {
		return "", fmt.Errorf("git repository URL is empty")
	}

	repoSlug := strings.ReplaceAll(app.Name, "/", "-")
	buildDir := filepath.Join(b.workDir, repoSlug)
	_ = os.RemoveAll(buildDir) // Clean old build context

	branch := app.GitBranch
	if branch == "" {
		branch = "main"
	}

	// 1. Git Clone
	cloneArgs := []string{"clone", "--depth", "1", "-b", branch, app.GitRepo, buildDir}
	cmdClone := exec.CommandContext(ctx, "git", cloneArgs...)
	outClone, err := cmdClone.CombinedOutput()
	if err != nil {
		// Fallback without branch flag if branch doesn't exist
		cmdFallback := exec.CommandContext(ctx, "git", "clone", "--depth", "1", app.GitRepo, buildDir)
		outFallback, errFallback := cmdFallback.CombinedOutput()
		if errFallback != nil {
			return "", fmt.Errorf("git clone failed: %s (%w)", string(outFallback), errFallback)
		}
	}
	_ = outClone

	// 2. Determine Dockerfile or generate one
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		// Auto-detect framework & generate minimal Dockerfile
		if err := b.detectAndGenerateDockerfile(buildDir); err != nil {
			return "", fmt.Errorf("no Dockerfile found and auto-generate failed: %w", err)
		}
	}

	// 3. Build Docker Image locally
	imageTag := fmt.Sprintf("fleet-%s:%d", app.Name, time.Now().Unix())
	cmdBuild := exec.CommandContext(ctx, "docker", "build", "-t", imageTag, buildDir)
	outBuild, err := cmdBuild.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker build failed: %s (%w)", string(outBuild), err)
	}

	return imageTag, nil
}

func (b *GitBuilder) detectAndGenerateDockerfile(dir string) error {
	// Node / Static Web
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		content := `FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build || true

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
`
		return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0644)
	}

	// Static HTML site
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		content := `FROM nginx:alpine
COPY . /usr/share/nginx/html
EXPOSE 80
`
		return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0644)
	}

	// Go project
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		content := `FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -o /app/server .

FROM alpine:3.21
COPY --from=builder /app/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
`
		return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0644)
	}

	return fmt.Errorf("could not auto-detect framework in repository")
}
