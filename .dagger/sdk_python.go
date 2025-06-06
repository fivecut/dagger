package main

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	pythonSubdir           = "sdk/python"
	pythonRuntimeSubdir    = "sdk/python/runtime"
	pythonGeneratedAPIPath = "sdk/python/src/dagger/client/gen.py"
)

type PythonSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the Python SDK
func (t PythonSDK) Lint(ctx context.Context) (rerr error) {
	eg := errgroup.Group{}

	// TODO: create function in PythonSDKDev to lint any directory as input
	// but reusing the same linter configuration in the SDK.
	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint Python code in the SDK and docs")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		// Preserve same file hierarchy for docs because of extend rules in .ruff.toml
		_, err := dag.PythonSDKDev().
			WithDirectory(
				dag.Directory().
					WithDirectory(
						"",
						t.Dagger.Source,
						dagger.DirectoryWithDirectoryOpts{
							Include: []string{
								"docs/current_docs/**/*.py",
								"**/.ruff.toml",
							},
						},
					),
			).
			Lint(ctx, dagger.PythonSDKDevLintOpts{Paths: []string{"../.."}})

		return err
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that the generated client library is up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := t.Dagger.Source
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{pythonGeneratedAPIPath})
	})

	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the python runtime, which is written in Go")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		return dag.
			Go(t.Dagger.SourceDeveloped(pythonRuntimeSubdir)).
			Lint(ctx, dagger.GoLintOpts{Packages: []string{pythonRuntimeSubdir}})
	})

	return eg.Wait()
}

// Test the Python SDK
func (t PythonSDK) Test(ctx context.Context) (rerr error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	base := dag.PythonSDKDev().Container().With(installer)
	dev := dag.PythonSDKDev(dagger.PythonSDKDevOpts{Container: base})

	versions, err := dag.PythonSDKDev().SupportedVersions(ctx)
	if err != nil {
		return err
	}

	eg := errgroup.Group{}
	for _, version := range versions {
		eg.Go(func() error {
			_, err := dev.
				Test(dagger.PythonSDKDevTestOpts{Version: version}).
				Default().
				Sync(ctx)
			return err
		})
	}

	return eg.Wait()
}

// Regenerate the Python SDK API
func (t PythonSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return nil, err
	}
	introspection, err := t.Dagger.introspection(ctx, installer)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithDirectory(
		pythonSubdir,
		dag.PythonSDKDev().Generate(introspection),
	), nil
}

// Test the publishing process
func (t PythonSDK) TestPublish(ctx context.Context, tag string) error {
	return t.Publish(ctx, tag, true, "", nil)
}

// Publish the Python SDK
func (t PythonSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,

	// +optional
	pypiRepo string,
	// +optional
	pypiToken *dagger.Secret,
) error {
	version := strings.TrimPrefix(tag, "sdk/python/")

	var ctr *dagger.Container
	if dryRun {
		ctr = dag.PythonSDKDev().Build()
	} else {
		opts := dagger.PythonSDKDevPublishOpts{
			Version: strings.TrimPrefix(version, "v"),
		}
		if pypiRepo == "test" {
			opts.URL = "https://test.pypi.org/legacy/"
		}
		ctr = dag.PythonSDKDev().Publish(pypiToken, opts)
	}
	_, err := ctr.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Bump the Python SDK's Engine dependency
func (t PythonSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	// trim leading v from version
	version = strings.TrimPrefix(version, "v")
	engineReference := fmt.Sprintf("# Code generated by dagger. DO NOT EDIT.\n\nCLI_VERSION = %q\n", version)

	// NOTE: if you change this path, be sure to update .github/workflows/publish.yml so that
	// provision tests run whenever this file changes.
	return dag.Directory().WithNewFile("sdk/python/src/dagger/_engine/_version.py", engineReference), nil
}
