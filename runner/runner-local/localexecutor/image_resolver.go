package localexecutor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/marginlab/margin-eval/runner/runner-core/imageresolver"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

type localDockerImageResolver struct {
	dockerBinary string

	mu       sync.Mutex
	inflight map[string]*buildCall
}

type buildCall struct {
	done  chan struct{}
	image string
	err   error
}

func newLocalDockerImageResolver(dockerBinary string) (*localDockerImageResolver, error) {
	binary := strings.TrimSpace(dockerBinary)
	if binary == "" {
		binary = defaultDockerBinary
	}
	return &localDockerImageResolver{
		dockerBinary: binary,
		inflight:     map[string]*buildCall{},
	}, nil
}

func (r *localDockerImageResolver) Resolve(ctx context.Context, in imageresolver.ResolveInput) (string, error) {
	return r.ResolveWithBuildLog(ctx, in, nil)
}

func (r *localDockerImageResolver) ResolveWithBuildLog(ctx context.Context, in imageresolver.ResolveInput, buildLog io.Writer) (string, error) {
	explicitImage := strings.TrimSpace(in.Image)
	if explicitImage != "" && in.ImageBuild == nil {
		return explicitImage, nil
	}
	if in.ImageBuild == nil {
		return "", fmt.Errorf("%w: case %q must provide image or image_build", imageresolver.ErrInvalidBuildSpec, in.CaseID)
	}
	if explicitImage != "" {
		exists, err := r.imageExists(ctx, explicitImage)
		if err != nil {
			return "", fmt.Errorf("inspect case %q image %q: %w", in.CaseID, explicitImage, err)
		}
		if exists {
			return explicitImage, nil
		}
	}
	buildKey := strings.TrimSpace(imageresolver.BuildKey(in))
	if buildKey == "" {
		return "", fmt.Errorf("%w: case %q has empty build key", imageresolver.ErrInvalidBuildSpec, in.CaseID)
	}

	return r.doSingleflight(ctx, buildKey, func() (string, error) {
		return r.resolveByBuilding(ctx, in, buildKey, buildLog)
	})
}

func (r *localDockerImageResolver) doSingleflight(ctx context.Context, key string, fn func() (string, error)) (string, error) {
	r.mu.Lock()
	if call, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return call.image, call.err
	}
	call := &buildCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.mu.Unlock()

	call.image, call.err = fn()
	close(call.done)

	r.mu.Lock()
	delete(r.inflight, key)
	r.mu.Unlock()
	return call.image, call.err
}

func (r *localDockerImageResolver) resolveByBuilding(ctx context.Context, in imageresolver.ResolveInput, buildKey string, buildLog io.Writer) (string, error) {
	tag := buildTagFromBuildKey(buildKey)

	if cachedRef, err := r.inspectDigestReference(ctx, tag); err == nil {
		return cachedRef, nil
	}

	tempDir, err := os.MkdirTemp("", "marginlab-image-build-*")
	if err != nil {
		return "", fmt.Errorf("create image build dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := testassets.Materialize(in.ImageBuild.Context, tempDir, maxTestAssetsArchiveByte); err != nil {
		return "", fmt.Errorf("%w: case %q invalid build context: %v", imageresolver.ErrInvalidBuildSpec, in.CaseID, err)
	}

	dockerfileRel := strings.TrimSpace(in.ImageBuild.DockerfileRelPath)
	if dockerfileRel == "" {
		return "", fmt.Errorf("%w: case %q missing dockerfile_rel_path", imageresolver.ErrInvalidBuildSpec, in.CaseID)
	}
	dockerfilePath := filepath.Join(tempDir, filepath.FromSlash(dockerfileRel))
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("%w: case %q missing dockerfile %q: %v", imageresolver.ErrInvalidBuildSpec, in.CaseID, dockerfileRel, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: case %q dockerfile path %q must be a file", imageresolver.ErrInvalidBuildSpec, in.CaseID, dockerfileRel)
	}

	if _, err := r.runDockerWithWriter(ctx, buildLog, "build", "-t", tag, "-f", dockerfilePath, tempDir); err != nil {
		return "", fmt.Errorf("build case %q image: %w", in.CaseID, err)
	}
	imageRef, err := r.inspectDigestReference(ctx, tag)
	if err != nil {
		return "", fmt.Errorf("resolve digest for case %q image %q: %w", in.CaseID, tag, err)
	}
	return imageRef, nil
}

func (r *localDockerImageResolver) Cleanup(ctx context.Context, in imageresolver.ResolveInput, resolvedImage string) error {
	if in.ImageBuild == nil {
		return nil
	}
	buildKey := strings.TrimSpace(imageresolver.BuildKey(in))
	if buildKey == "" {
		return nil
	}
	refs := make([]string, 0, 2)
	if image := strings.TrimSpace(resolvedImage); image != "" {
		refs = append(refs, image)
	}
	refs = append(refs, buildTagFromBuildKey(buildKey))

	seen := map[string]struct{}{}
	var errs []string
	for _, ref := range refs {
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		if _, err := r.runDocker(ctx, "image", "rm", "--force", trimmed); err != nil && !isDockerImageNotFound(err) {
			errs = append(errs, fmt.Sprintf("%s: %v", trimmed, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup built image refs: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *localDockerImageResolver) inspectDigestReference(ctx context.Context, tag string) (string, error) {
	repo := imageRepository(tag)
	digestsRaw, err := r.runDocker(ctx, "image", "inspect", tag, "--format", "{{json .RepoDigests}}")
	if err != nil {
		return "", err
	}

	var digests []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(digestsRaw)), &digests); err != nil {
		return "", fmt.Errorf("decode docker image repo digests for %q: %w", tag, err)
	}
	for _, digest := range digests {
		trimmed := strings.TrimSpace(digest)
		if strings.HasPrefix(trimmed, repo+"@sha256:") {
			return trimmed, nil
		}
	}
	for _, digest := range digests {
		trimmed := strings.TrimSpace(digest)
		if strings.Contains(trimmed, "@sha256:") {
			return trimmed, nil
		}
	}

	idRaw, err := r.runDocker(ctx, "image", "inspect", tag, "--format", "{{.Id}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(idRaw)
	if !strings.HasPrefix(id, "sha256:") {
		return "", fmt.Errorf("unexpected docker image id format %q", id)
	}

	// Locally built images commonly have no RepoDigests. In that case, the
	// immutable local image ID is the runnable pinned reference; synthesizing
	// repo@sha256:<image-id> causes Docker to treat it as a remote digest ref.
	return id, nil
}

func (r *localDockerImageResolver) imageExists(ctx context.Context, imageRef string) (bool, error) {
	_, err := r.runDocker(ctx, "image", "inspect", imageRef, "--format", "{{.Id}}")
	if err == nil {
		return true, nil
	}
	if isDockerImageNotFound(err) {
		return false, nil
	}
	return false, err
}

func (r *localDockerImageResolver) runDocker(ctx context.Context, args ...string) (string, error) {
	return r.runDockerWithWriter(ctx, nil, args...)
}

func (r *localDockerImageResolver) runDockerWithWriter(ctx context.Context, logWriter io.Writer, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.dockerBinary, args...)
	var out bytes.Buffer
	if logWriter != nil {
		multi := newSynchronizedWriter(io.MultiWriter(&out, logWriter))
		cmd.Stdout = multi
		cmd.Stderr = multi
	} else {
		writer := newSynchronizedWriter(&out)
		cmd.Stdout = writer
		cmd.Stderr = writer
	}
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

func buildTagFromBuildKey(buildKey string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(buildKey)))
	return fmt.Sprintf("marginlab-local/buildctx:sha-%s", hex.EncodeToString(hash[:])[:16])
}

func isDockerImageNotFound(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "no such image") ||
		strings.Contains(msg, "no such object") ||
		strings.Contains(msg, "image not known") ||
		strings.Contains(msg, "reference does not exist")
}

func imageRepository(imageRef string) string {
	lastColon := strings.LastIndex(imageRef, ":")
	lastSlash := strings.LastIndex(imageRef, "/")
	if lastColon > lastSlash {
		return imageRef[:lastColon]
	}
	return imageRef
}
