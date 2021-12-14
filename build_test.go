package nodeengine_test

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	nodeengine "github.com/paketo-buildpacks/node-engine"
	"github.com/paketo-buildpacks/node-engine/fakes"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/postal"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
)

func testBuild(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect = NewWithT(t).Expect

		workingDir        string
		layersDir         string
		cnbDir            string
		entryResolver     *fakes.EntryResolver
		dependencyManager *fakes.DependencyManager
		sbomGenerator     *fakes.SBOMGenerator
		clock             chronos.Clock
		timeStamp         time.Time
		environment       *fakes.EnvironmentConfiguration
		buffer            *bytes.Buffer

		build        packit.BuildFunc
		buildContext packit.BuildContext
	)

	it.Before(func() {
		var err error
		layersDir, err = ioutil.TempDir("", "layers")
		Expect(err).NotTo(HaveOccurred())

		cnbDir, err = ioutil.TempDir("", "cnb")
		Expect(err).NotTo(HaveOccurred())

		workingDir, err = ioutil.TempDir("", "working-dir")
		Expect(err).NotTo(HaveOccurred())

		err = ioutil.WriteFile(filepath.Join(cnbDir, "buildpack.toml"), []byte(`api = "0.2"
[buildpack]
  id = "org.some-org.some-buildpack"
  name = "Some Buildpack"
  version = "some-version"
  sbom-formats = ["cdx","spdx"]

[metadata]
  [metadata.default-versions]
    node = "10.x"

  [[metadata.dependencies]]
    deprecation_date = 2021-04-01T00:00:00Z
    id = "some-dep"
    name = "Some Dep"
    sha256 = "some-sha"
    stacks = ["some-stack"]
    uri = "some-uri"
    version = "some-dep-version"
`), 0644)
		Expect(err).NotTo(HaveOccurred())

		entryResolver = &fakes.EntryResolver{}
		entryResolver.ResolveCall.Returns.BuildpackPlanEntry = packit.BuildpackPlanEntry{
			Name: "node",
			Metadata: map[string]interface{}{
				"version":        "~10",
				"version-source": "BP_NODE_VERSION",
			},
		}
		entryResolver.MergeLayerTypesCall.Returns.Launch = false
		entryResolver.MergeLayerTypesCall.Returns.Build = false

		dependencyManager = &fakes.DependencyManager{}
		dependencyManager.ResolveCall.Returns.Dependency = postal.Dependency{Name: "Node Engine"}

		sbomGenerator = &fakes.SBOMGenerator{}
		sbomGenerator.GenerateFromDependencyCall.Returns.SBOM = sbom.SBOM{}

		environment = &fakes.EnvironmentConfiguration{}

		timeStamp = time.Now()
		clock = chronos.NewClock(func() time.Time {
			return timeStamp
		})

		buffer = bytes.NewBuffer(nil)
		logEmitter := nodeengine.NewLogEmitter(buffer)

		build = nodeengine.Build(entryResolver, dependencyManager, environment, sbomGenerator, logEmitter, clock)

		buildContext = packit.BuildContext{
			CNBPath: cnbDir,
			Stack:   "some-stack",
			BuildpackInfo: packit.BuildpackInfo{
				Name:        "Some Buildpack",
				Version:     "1.2.3",
				SBOMFormats: []string{sbom.CycloneDXFormat, sbom.SPDXFormat},
			},
			Plan: packit.BuildpackPlan{
				Entries: []packit.BuildpackPlanEntry{
					{
						Name: "node",
						Metadata: map[string]interface{}{
							"version":        "~10",
							"version-source": "BP_NODE_VERSION",
						},
					},
				},
			},
			Platform:   packit.Platform{Path: "platform"},
			Layers:     packit.Layers{Path: layersDir},
			WorkingDir: workingDir,
		}

	})

	it.After(func() {
		Expect(os.RemoveAll(layersDir)).To(Succeed())
		Expect(os.RemoveAll(cnbDir)).To(Succeed())
		Expect(os.RemoveAll(workingDir)).To(Succeed())
	})

	it("returns a result that installs node", func() {
		result, err := build(buildContext)
		Expect(err).NotTo(HaveOccurred())

		Expect(result.Layers).To(HaveLen(1))
		layer := result.Layers[0]

		Expect(layer.Name).To(Equal("node"))
		Expect(layer.Path).To(Equal(filepath.Join(layersDir, "node")))
		Expect(layer.Metadata).To(Equal(map[string]interface{}{
			nodeengine.DepKey: "",
			"built_at":        timeStamp.Format(time.RFC3339Nano),
		}))

		Expect(layer.SBOM.Formats()).To(Equal([]packit.SBOMFormat{
			{
				Extension: sbom.Format(sbom.CycloneDXFormat).Extension(),
				Content:   sbom.NewFormattedReader(sbom.SBOM{}, sbom.CycloneDXFormat),
			},
			{
				Extension: sbom.Format(sbom.SPDXFormat).Extension(),
				Content:   sbom.NewFormattedReader(sbom.SBOM{}, sbom.SPDXFormat),
			},
		}))

		Expect(filepath.Join(layersDir, "node")).To(BeADirectory())

		Expect(entryResolver.ResolveCall.Receives.Entries).To(Equal([]packit.BuildpackPlanEntry{
			{
				Name: "node",
				Metadata: map[string]interface{}{
					"version":        "~10",
					"version-source": "BP_NODE_VERSION",
				},
			},
		}))
		Expect(entryResolver.MergeLayerTypesCall.Receives.Name).To(Equal("node"))
		Expect(entryResolver.MergeLayerTypesCall.Receives.Entries).To(Equal(
			[]packit.BuildpackPlanEntry{
				{
					Name: "node",
					Metadata: map[string]interface{}{
						"version":        "~10",
						"version-source": "BP_NODE_VERSION",
					},
				},
			}))
		Expect(dependencyManager.ResolveCall.Receives.Path).To(Equal(filepath.Join(cnbDir, "buildpack.toml")))
		Expect(dependencyManager.ResolveCall.Receives.Id).To(Equal("node"))
		Expect(dependencyManager.ResolveCall.Receives.Version).To(Equal("~10"))
		Expect(dependencyManager.ResolveCall.Receives.Stack).To(Equal("some-stack"))

		Expect(dependencyManager.DeliverCall.Receives.Dependency).To(Equal(postal.Dependency{Name: "Node Engine"}))
		Expect(dependencyManager.DeliverCall.Receives.CnbPath).To(Equal(cnbDir))
		Expect(dependencyManager.DeliverCall.Receives.LayerPath).To(Equal(filepath.Join(layersDir, "node")))
		Expect(dependencyManager.DeliverCall.Receives.PlatformPath).To(Equal("platform"))

		Expect(sbomGenerator.GenerateFromDependencyCall.Receives.Dependency).To(Equal(postal.Dependency{Name: "Node Engine"}))
		Expect(sbomGenerator.GenerateFromDependencyCall.Receives.Dir).To(Equal(workingDir))

		Expect(environment.ConfigureCall.Receives.BuildEnv).To(Equal(packit.Environment{}))
		Expect(environment.ConfigureCall.Receives.LaunchEnv).To(Equal(packit.Environment{}))
		Expect(environment.ConfigureCall.Receives.Path).To(Equal(filepath.Join(layersDir, "node")))
		Expect(environment.ConfigureCall.Receives.OptimizeMemory).To(BeFalse())

		Expect(buffer.String()).To(ContainSubstring("Some Buildpack 1.2.3"))
		Expect(buffer.String()).To(ContainSubstring("Resolving Node Engine version"))
		Expect(buffer.String()).To(ContainSubstring("Selected Node Engine version (using BP_NODE_VERSION): "))
		Expect(buffer.String()).To(ContainSubstring("Executing build process"))
	})

	context("when the os environment contains a directive to optimize memory", func() {
		it.Before(func() {
			Expect(os.Setenv("BP_NODE_OPTIMIZE_MEMORY", "true")).To(Succeed())
		})

		it.After(func() {
			Expect(os.Unsetenv("BP_NODE_OPTIMIZE_MEMORY")).To(Succeed())
		})

		it("tells the environment to optimize memory", func() {
			_, err := build(buildContext)
			Expect(err).NotTo(HaveOccurred())

			Expect(environment.ConfigureCall.Receives.BuildEnv).To(Equal(packit.Environment{}))
			Expect(environment.ConfigureCall.Receives.LaunchEnv).To(Equal(packit.Environment{}))
			Expect(environment.ConfigureCall.Receives.Path).To(Equal(filepath.Join(layersDir, "node")))
			Expect(environment.ConfigureCall.Receives.OptimizeMemory).To(BeTrue())
		})
	})

	context("when the build plan entry includes the build, launch flags", func() {
		var workingDir string

		it.Before(func() {
			var err error
			workingDir, err = ioutil.TempDir("", "working-dir")
			Expect(err).NotTo(HaveOccurred())

			entryResolver.ResolveCall.Returns.BuildpackPlanEntry = packit.BuildpackPlanEntry{
				Name: "node",
				Metadata: map[string]interface{}{
					"version":        "~10",
					"version-source": "BP_NODE_VERSION",
				},
			}

			entryResolver.MergeLayerTypesCall.Returns.Launch = true
			entryResolver.MergeLayerTypesCall.Returns.Build = true
		})

		it.After(func() {
			Expect(os.RemoveAll(workingDir)).To(Succeed())
		})

		it("marks the node layer as build, cached and launch", func() {
			result, err := build(buildContext)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Layers).To(HaveLen(1))
			layer := result.Layers[0]

			Expect(layer.Name).To(Equal("node"))
			Expect(layer.Build).To(BeTrue())
			Expect(layer.Launch).To(BeTrue())
			Expect(layer.Cache).To(BeTrue())
		})
	})

	context("when there is a dependency cache match", func() {
		it.Before(func() {
			err := ioutil.WriteFile(filepath.Join(layersDir, "node.toml"), []byte("[metadata]\ndependency-sha = \"some-sha\"\n"), 0644)
			Expect(err).NotTo(HaveOccurred())

			dependencyManager.ResolveCall.Returns.Dependency = postal.Dependency{
				Name:   "Node Engine",
				SHA256: "some-sha",
			}
		})

		it("exits build process early", func() {
			_, err := build(buildContext)
			Expect(err).NotTo(HaveOccurred())

			Expect(sbomGenerator.GenerateFromDependencyCall.CallCount).To(Equal(0))
			Expect(dependencyManager.DeliverCall.CallCount).To(Equal(0))
			Expect(environment.ConfigureCall.CallCount).To(Equal(0))

			Expect(buffer.String()).To(ContainSubstring("Some Buildpack 1.2.3"))
			Expect(buffer.String()).To(ContainSubstring("Resolving Node Engine version"))
			Expect(buffer.String()).To(ContainSubstring("Selected Node Engine version (using BP_NODE_VERSION): "))
			Expect(buffer.String()).To(ContainSubstring("Reusing cached layer"))
			Expect(buffer.String()).ToNot(ContainSubstring("Executing build process"))
		})
	})

	context("failure cases", func() {
		context("when a dependency cannot be resolved", func() {
			it.Before(func() {
				dependencyManager.ResolveCall.Returns.Error = errors.New("failed to resolve dependency")
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError("failed to resolve dependency"))
			})
		})

		context("when a dependency cannot be installed", func() {
			it.Before(func() {
				dependencyManager.DeliverCall.Returns.Error = errors.New("failed to install dependency")
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError("failed to install dependency"))
			})
		})

		context("when generating the SBOM returns an error", func() {
			it.Before(func() {
				buildContext.BuildpackInfo.SBOMFormats = []string{"random-format"}
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError("\"random-format\" is not a supported SBOM format"))
			})
		})

		context("when formatting the SBOM returns an error", func() {
			it.Before(func() {
				sbomGenerator.GenerateFromDependencyCall.Returns.Error = errors.New("failed to generate SBOM")
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError(ContainSubstring("failed to generate SBOM")))
			})
		})

		context("when the layers directory cannot be written to", func() {
			it.Before(func() {
				Expect(os.Chmod(layersDir, 0000)).To(Succeed())
			})

			it.After(func() {
				Expect(os.Chmod(layersDir, os.ModePerm)).To(Succeed())
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError(ContainSubstring("permission denied")))
			})
		})

		context("when the layer directory cannot be removed", func() {
			var layerDir string
			it.Before(func() {
				layerDir = filepath.Join(layersDir, nodeengine.Node)
				Expect(os.MkdirAll(filepath.Join(layerDir, "baller"), os.ModePerm)).To(Succeed())
				Expect(os.Chmod(layerDir, 0000)).To(Succeed())
			})

			it.After(func() {
				Expect(os.Chmod(layerDir, os.ModePerm)).To(Succeed())
				Expect(os.RemoveAll(layerDir)).To(Succeed())
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError(ContainSubstring("permission denied")))
			})
		})

		context("when the environment cannot be configured", func() {
			it.Before(func() {
				environment.ConfigureCall.Returns.Error = errors.New("failed to configure environment")
			})

			it("returns an error", func() {
				_, err := build(buildContext)
				Expect(err).To(MatchError("failed to configure environment"))
			})
		})
	})
}
