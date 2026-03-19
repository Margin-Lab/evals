package imageresolver

import (
	"context"
	"errors"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

var (
	ErrImageBuildUnsupported = errors.New("image build is not supported by this runner")
	ErrInvalidBuildSpec      = errors.New("invalid image build specification")
)

type ResolveInput struct {
	CaseID     string
	Image      string
	ImageBuild *runbundle.CaseImageBuild
}

type Resolver interface {
	Resolve(ctx context.Context, in ResolveInput) (string, error)
}

type Cleaner interface {
	Cleanup(ctx context.Context, in ResolveInput, resolvedImage string) error
}

func BuildKey(in ResolveInput) string {
	if in.ImageBuild == nil {
		return ""
	}
	dockerfile := strings.TrimSpace(in.ImageBuild.DockerfileRelPath)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	return in.ImageBuild.Context.ArchiveTGZSHA256 + ":" + dockerfile
}
