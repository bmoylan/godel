// Copyright 2016 Palantir Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"encoding/json"
	"io/ioutil"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/palantir/pkg/matcher"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/palantir/godel/apps/distgo/params"
	"github.com/palantir/godel/apps/distgo/pkg/osarch"
)

type Project struct {
	// Products maps product names to configurations.
	Products map[string]Product `yaml:"products" json:"products"`

	// BuildOutputDir specifies the default build output directory for products executables built by the "build"
	// command. The executables generated by "build" will be written to this directory unless the location is
	// overridden by the product-specific configuration.
	BuildOutputDir string `yaml:"build-output-dir" json:"build-output-dir"`

	// DistOutputDir specifies the default distribution output directory for product distributions created by the
	// "dist" command. The distribution directory and artifact generated by "dist" will be written to this directory
	// unless the location is overridden by the product-specific configuration.
	DistOutputDir string `yaml:"dist-output-dir" json:"dist-output-dir"`

	// DistScriptInclude is script content that is prepended to any non-empty ProductDistCfg.Script. It can be used
	// to define common functionality used in the distribution script for multiple different products.
	DistScriptInclude string `yaml:"dist-script-include" json:"dist-script-include"`

	// GroupID is the identifier used as the group ID for the POM.
	GroupID string `yaml:"group-id" json:"group-id"`

	// Exclude matches the paths to exclude when determining the projects to build.
	Exclude matcher.NamesPathsCfg `yaml:"exclude" json:"exclude"`
}

// Product represents user-specified configuration on how to build a specific product.
type Product struct {
	// Build specifies the build configuration for the product.
	Build Build `yaml:"build" json:"build"`

	// Run specifies the run configuration for the product.
	Run Run `yaml:"run" json:"run"`

	// Dist specifies the dist configurations for the product.
	Dist RawDistConfigs `yaml:"dist" json:"dist"`

	// DefaultPublish specifies the publish configuration that is applied to distributions that do not specify their
	// own publish configurations.
	DefaultPublish Publish `yaml:"publish" json:"publish"`
}

type Build struct {
	// Script is the content of a script that is written to file a file and run before this product is built. The
	// contents of this value are written to a file with a header `#!/bin/bash` and executed. The script process
	// inherits the environment variables of the Go process and also has the following environment variables
	// defined:
	//
	//   PROJECT_DIR: the root directory of project
	//   PRODUCT: product name,
	//   VERSION: product version
	//   IS_SNAPSHOT: 1 if the version contains a git hash as part of the string, 0 otherwise
	Script string `yaml:"script" json:"script"`

	// MainPkg is the location of the main package for the product relative to the root directory. For example,
	// "./distgo/main".
	MainPkg string `yaml:"main-pkg" json:"main-pkg"`

	// OutputDir is the directory to which the executable is written.
	OutputDir string `yaml:"output-dir" json:"output-dir"`

	// BuildArgsScript is the content of a script that is written to a file and run before this product is built
	// to provide supplemental build arguments for the product. The contents of this value are written to a file
	// with a header `#!/bin/bash` and executed. The script process inherits the environment variables of the Go
	// process. Each line of output of the script is provided to the "build" command as a separate argument. For
	// example, the following script would add the arguments "-ldflags" "-X" "main.year=$YEAR" to the build command:
	//
	//   build-args-script: |
	//                      YEAR=$(date +%Y)
	//                      echo "-ldflags"
	//                      echo "-X"
	//                      echo "main.year=$YEAR"
	BuildArgsScript string `yaml:"build-args-script" json:"build-args-script"`

	// VersionVar is the path to a variable that is set with the version information for the build. For example,
	// "github.com/palantir/godel/cmd/godel.Version". If specified, it is provided to the "build" command as an
	// ldflag.
	VersionVar string `yaml:"version-var" json:"version-var"`

	// Environment specifies values for the environment variables that should be set for the build. For example,
	// the following sets CGO to false:
	//
	//   environment:
	//     CGO_ENABLED: "0"
	Environment map[string]string `yaml:"environment" json:"environment"`

	// OSArchs specifies the GOOS and GOARCH pairs for which the product is built. If blank, defaults to the GOOS
	// and GOARCH of the host system at runtime.
	OSArchs []osarch.OSArch `yaml:"os-archs" json:"os-archs"`
}

type Run struct {
	// Args contain the arguments provided to the product when invoked using the "run" task.
	Args []string `yaml:"args" json:"args"`
}

type Dist struct {
	// OutputDir is the directory to which the distribution is written.
	OutputDir string `yaml:"output-dir" json:"output-dir"`

	// InputDir is the path (from the project root) to a directory whose contents will be copied into the output
	// distribution directory at the beginning of the "dist" command. Can be used to include static resources and
	// other files required in a distribution.
	InputDir string `yaml:"input-dir" json:"input-dir"`

	// InputProducts is a slice of the names of products in the project (other than the current one) whose binaries
	// are required for the "dist" task. The "dist" task will ensure that the outputs of "build" exist for all of
	// the products specified in this slice (and will build the products as part of the task if necessary) and make
	// the outputs available to the "dist" script as environment variables. Note that the "dist" task only
	// guarantees that the products will be built and their locations will be available in the environment variables
	// provided to the script -- it is the responsibility of the user to write logic in the dist script to copy the
	// generated binaries.
	InputProducts []string `yaml:"input-products" json:"input-products"`

	// Script is the content of a script that is written to file a file and run after the initial distribution
	// process but before the artifact generation process. The contents of this value are written to a file with a
	// header `#!/bin/bash` with the contents of the global `dist-script-include` prepended and executed. The script
	// process inherits the environment variables of the Go process and also has the following environment variables
	// defined:
	//
	//   DIST_DIR: the absolute path to the root directory of the distribution created for the current product
	//   PROJECT_DIR: the root directory of project
	//   PRODUCT: product name,
	//   VERSION: product version
	//   IS_SNAPSHOT: 1 if the version contains a git hash as part of the string, 0 otherwise
	Script string `yaml:"script" json:"script"`

	// DistType specifies the type of the distribution to be built and configuration for it. If unspecified,
	// defaults to a DistInfo of type SLSDistType.
	DistType DistInfo `yaml:"dist-type" json:"dist-type"`

	// Publish is the configuration for the "publish" task.
	Publish Publish `yaml:"publish" json:"publish"`
}

type DistInfo struct {
	// Type is the type of the distribution. Value should be a valid value defined by params.DistInfoType.
	Type string `yaml:"type" json:"type"`

	// Info is the configuration content of the dist info.
	Info interface{} `yaml:"info" json:"info"`
}

type BinDist struct {
	// OmitInitSh specifies whether or not the distribution should omit the auto-generated "init.sh" invocation
	// script. If true, the "init.sh" script will not be generated and included in the output distribution.
	OmitInitSh bool `yaml:"omit-init-sh" json:"omit-init-sh"`
	// InitShTemplateFile is the relative path to the template that should be used to generate the "init.sh" script.
	// If the value is absent, the default template will be used.
	InitShTemplateFile string `yaml:"init-sh-template-file" json:"init-sh-template-file"`
}

type DockerDist struct {
	// ImageName specifies the name to use for this container, may include a tag.
	ImageName string `yaml:"image-name" json:"image-name"`
	// Tags spcifies a list of tags to create; any tag in ImageName will be stripped before applying a specific tag.
	Tags []string `yaml:"tags" json:"tags"`

	// Dockerfile specifies the dockerfile to use for building the image; defaults to $PROJECT_DIR/Dockerfile
	Dockerfile string `yaml:"dockerfile" json:"dockerfile"`
	// BuildArgs is a map[string]string which will set --build-arg arguments to the docker build command.
	BuildArgs map[string]string `yaml:"build-args" json:"build-args"`
	// Labels is a map[string]string which will set --label arguments to the docker build command.
	Labels map[string]string `yaml:"labels" json:"labels"`
	// Labels is a map[string]string which will set --labels arguments to the docker build command.
	// Files specifies additional files or directories to add to the docker context.
	Files []string `yaml:"files" json:"files"`
	// ForcePull is a boolean which defines whether Docker should attempt to pull a newer version of the base image
	// before building.
	ForcePull bool `yaml:"force-pull" json:"force-pull"`
}

type SLSDist struct {
	// InitShTemplateFile is the path to a template file that is used as the basis for the init.sh script of the
	// distribution. The path is relative to the project root directory. The contents of the file is processed using
	// Go templates and is provided with a distgo.ProductBuildSpec struct. If omitted, the default init.sh script
	// is used.
	InitShTemplateFile string `yaml:"init-sh-template-file" json:"init-sh-template-file"`

	// ManifestTemplateFile is the path to a template file that is used as the basis for the manifest.yml file of
	// the distribution. The path is relative to the project root directory. The contents of the file is processed
	// using Go templates and is provided with a distgo.ProductBuildSpec struct.
	ManifestTemplateFile string `yaml:"manifest-template-file" json:"manifest-template-file"`

	// ServiceArgs is the string provided as the service arguments for the default init.sh file generated for the distribution.
	ServiceArgs string `yaml:"service-args" json:"service-args"`

	// ProductType is the SLS product type for the distribution.
	ProductType string `yaml:"product-type" json:"product-type"`

	// ManifestExtensions contain the SLS manifest extensions for the distribution.
	ManifestExtensions map[string]interface{} `yaml:"manifest-extensions" json:"manifest-extensions"`

	// YMLValidationExclude specifies a matcher used to specify YML files or paths that should not be validated as
	// part of creating the distribution. By default, the SLS distribution task verifies that all "*.yml" and
	// "*.yaml" files in the distribution are syntactically valid. If a distribution is known to ship with YML files
	// that are not valid YML, this parameter can be used to exclude those files from validation.
	YMLValidationExclude matcher.NamesPathsCfg `yaml:"yml-validation-exclude" json:"yml-validation-exclude"`
}

type RPMDist struct {
	// Release is the release identifier that forms part of the name/version/release/architecture quadruplet
	// uniquely identifying the RPM package. Default is "1".
	Release string `yaml:"release" json:"release"`

	// ConfigFiles is a slice of absolute paths within the RPM that correspond to configuration files. RPM
	// identifies these as mutable. Default is no files.
	ConfigFiles []string `yaml:"config-files" json:"config-files"`

	// BeforeInstallScript is the content of shell script to run before this RPM is installed. Optional.
	BeforeInstallScript string `yaml:"before-install-script" json:"before-install-script"`

	// AfterInstallScript is the content of shell script to run immediately after this RPM is installed. Optional.
	AfterInstallScript string `yaml:"after-install-script" json:"after-install-script"`

	// AfterRemoveScript is the content of shell script to clean up after this RPM is removed. Optional.
	AfterRemoveScript string `yaml:"after-remove-script" json:"after-remove-script"`
}

type Publish struct {
	// GroupID is the product-specific configuration equivalent to the global GroupID configuration.
	GroupID string `yaml:"group-id" json:"group-id"`

	// Almanac contains the parameters for Almanac publish operations. Optional.
	Almanac Almanac `yaml:"almanac" json:"almanac"`
}

type Almanac struct {
	// Metadata contains the metadata provided to the Almanac publish task.
	Metadata map[string]string `yaml:"metadata" json:"metadata"`

	// Tags contains the tags provided to the Almanac publish task.
	Tags []string `yaml:"tags" json:"tags"`
}

func (cfg *Project) ToParams() (params.Project, error) {
	products := make(map[string]params.Product, len(cfg.Products))
	for k, v := range cfg.Products {
		productParam, err := v.ToParam()
		if err != nil {
			return params.Project{}, err
		}
		products[k] = productParam
	}
	return params.Project{
		Products:          products,
		BuildOutputDir:    cfg.BuildOutputDir,
		DistOutputDir:     cfg.DistOutputDir,
		DistScriptInclude: cfg.DistScriptInclude,
		GroupID:           cfg.GroupID,
		Exclude:           cfg.Exclude.Matcher(),
	}, nil
}

func (cfg *Product) ToParam() (params.Product, error) {
	var dists []params.Dist
	for _, rawDistCfg := range cfg.Dist {
		dist, err := rawDistCfg.ToParam()
		if err != nil {
			return params.Product{}, err
		}
		dists = append(dists, dist)
	}

	return params.Product{
		Build:          cfg.Build.ToParam(),
		Run:            cfg.Run.ToParam(),
		Dist:           dists,
		DefaultPublish: cfg.DefaultPublish.ToParams(),
	}, nil
}

func (cfg *Build) ToParam() params.Build {
	return params.Build{
		Script:          cfg.Script,
		MainPkg:         cfg.MainPkg,
		OutputDir:       cfg.OutputDir,
		BuildArgsScript: cfg.BuildArgsScript,
		VersionVar:      cfg.VersionVar,
		Environment:     cfg.Environment,
		OSArchs:         cfg.OSArchs,
	}
}

type RawDistConfigs []Dist

func (out *RawDistConfigs) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var multiple []Dist
	if err := unmarshal(&multiple); err == nil {
		if len(multiple) == 0 {
			return errors.New("if `dist` key is specified, there must be at least one dist")
		}
		*out = multiple
		return nil
	}

	var single Dist
	if err := unmarshal(&single); err != nil {
		// return the error from a single DistConfig if neither one works
		return err
	}
	*out = []Dist{single}
	return nil
}

func (cfg *Dist) ToParam() (params.Dist, error) {
	info, err := cfg.DistType.ToParam()
	if err != nil {
		return params.Dist{}, err
	}
	return params.Dist{
		OutputDir:     cfg.OutputDir,
		InputDir:      cfg.InputDir,
		InputProducts: cfg.InputProducts,
		Script:        cfg.Script,
		Info:          info,
		Publish:       cfg.Publish.ToParams(),
	}, nil
}

func (cfg *Run) ToParam() params.Run {
	return params.Run{
		Args: cfg.Args,
	}
}

func (cfg *DistInfo) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// unmarshal type alias (uses default unmarshal strategy)
	type rawDistInfoConfigAlias DistInfo
	var rawAliasConfig rawDistInfoConfigAlias
	if err := unmarshal(&rawAliasConfig); err != nil {
		return err
	}

	rawDistInfoConfig := DistInfo(rawAliasConfig)
	switch params.DistInfoType(rawDistInfoConfig.Type) {
	case params.SLSDistType:
		type typedRawConfig struct {
			Type string
			Info SLSDist
		}
		var rawSLS typedRawConfig
		if err := unmarshal(&rawSLS); err != nil {
			return err
		}
		rawDistInfoConfig.Info = rawSLS.Info
	case params.BinDistType:
		type typedRawConfig struct {
			Type string
			Info BinDist
		}
		var rawBin typedRawConfig
		if err := unmarshal(&rawBin); err != nil {
			return err
		}
		rawDistInfoConfig.Info = rawBin.Info
	case params.DockerDistType:
		type typedRawConfig struct {
			Type string
			Info DockerDist
		}
		var rawDocker typedRawConfig
		if err := unmarshal(&rawDocker); err != nil {
			return err
		}
		rawDistInfoConfig.Info = rawDocker.Info
	case params.RPMDistType:
		type typedRawConfig struct {
			Type string
			Info RPMDist
		}
		var rawRPM typedRawConfig
		if err := unmarshal(&rawRPM); err != nil {
			return err
		}
		rawDistInfoConfig.Info = rawRPM.Info
	}
	*cfg = rawDistInfoConfig
	return nil
}

func (cfg *DistInfo) ToParam() (params.DistInfo, error) {
	var distInfo params.DistInfo
	if cfg.Info != nil {
		convertMapKeysToCamelCase(cfg.Info)
		var decodeErr error
		switch params.DistInfoType(cfg.Type) {
		case params.SLSDistType:
			val := SLSDist{}
			decodeErr = mapstructure.Decode(cfg.Info, &val)
			distInfo = &params.SLSDistInfo{
				InitShTemplateFile:   val.InitShTemplateFile,
				ManifestTemplateFile: val.ManifestTemplateFile,
				ServiceArgs:          val.ServiceArgs,
				ProductType:          val.ProductType,
				ManifestExtensions:   val.ManifestExtensions,
				YMLValidationExclude: val.YMLValidationExclude.Matcher(),
			}
		case params.BinDistType:
			val := BinDist{}
			decodeErr = mapstructure.Decode(cfg.Info, &val)
			distInfo = &params.BinDistInfo{
				OmitInitSh:         val.OmitInitSh,
				InitShTemplateFile: val.InitShTemplateFile,
			}
		case params.DockerDistType:
			val := DockerDist{}
			decodeErr = mapstructure.Decode(cfg.Info, &val)
			distInfo = &params.DockerDistInfo{
				ImageName:  val.ImageName,
				Tags:       val.Tags,
				BuildArgs:  val.BuildArgs,
				Dockerfile: val.Dockerfile,
				Files:      val.Files,
				ForcePull:  val.ForcePull,
			}
		case params.RPMDistType:
			val := RPMDist{}
			decodeErr = mapstructure.Decode(cfg.Info, &val)
			distInfo = &params.RPMDistInfo{
				Release:             val.Release,
				ConfigFiles:         val.ConfigFiles,
				BeforeInstallScript: val.BeforeInstallScript,
				AfterInstallScript:  val.AfterInstallScript,
				AfterRemoveScript:   val.AfterRemoveScript,
			}
		default:
			return nil, errors.Errorf("No unmarshaller found for type %s for %v", cfg.Type, *cfg)
		}
		if decodeErr != nil {
			return nil, errors.Wrapf(decodeErr, "failed to unmarshal DistTypeCfg.Info for %v", *cfg)
		}
	}
	return distInfo, nil
}

func convertMapKeysToCamelCase(input interface{}) {
	if inputMap, ok := input.(map[interface{}]interface{}); ok {
		for k, v := range inputMap {
			if str, ok := k.(string); ok {
				newStr := ""
				for _, currPart := range strings.Split(str, "-") {
					newStr += strings.ToUpper(currPart[0:1]) + currPart[1:]
				}
				delete(inputMap, k)
				inputMap[newStr] = v
			}
		}
	}
}

func (cfg *BinDist) ToParams() params.BinDistInfo {
	return params.BinDistInfo{
		OmitInitSh:         cfg.OmitInitSh,
		InitShTemplateFile: cfg.InitShTemplateFile,
	}
}

func (cfg *DockerDist) ToParams() params.DockerDistInfo {
	return params.DockerDistInfo{
		ImageName:  cfg.ImageName,
		Tags:       cfg.Tags,
		BuildArgs:  cfg.BuildArgs,
		Labels:     cfg.Labels,
		Dockerfile: cfg.Dockerfile,
		Files:      cfg.Files,
		ForcePull:  cfg.ForcePull,
	}
}

func (cfg *SLSDist) ToParams() params.SLSDistInfo {
	return params.SLSDistInfo{
		InitShTemplateFile:   cfg.InitShTemplateFile,
		ManifestTemplateFile: cfg.ManifestTemplateFile,
		ServiceArgs:          cfg.ServiceArgs,
		ProductType:          cfg.ProductType,
		ManifestExtensions:   cfg.ManifestExtensions,
		YMLValidationExclude: cfg.YMLValidationExclude.Matcher(),
	}
}

func (cfg *RPMDist) ToParams() params.RPMDistInfo {
	return params.RPMDistInfo{
		Release:             cfg.Release,
		ConfigFiles:         cfg.ConfigFiles,
		BeforeInstallScript: cfg.BeforeInstallScript,
		AfterInstallScript:  cfg.AfterInstallScript,
		AfterRemoveScript:   cfg.AfterRemoveScript,
	}
}

func (cfg *Publish) ToParams() params.Publish {
	return params.Publish{
		GroupID: cfg.GroupID,
		Almanac: cfg.Almanac.ToParams(),
	}
}

func (cfg *Almanac) ToParams() params.Almanac {
	return params.Almanac{
		Metadata: cfg.Metadata,
		Tags:     cfg.Tags,
	}
}

func Load(cfgPath, jsonContent string) (params.Project, error) {
	var yml []byte
	if cfgPath != "" {
		var err error
		yml, err = ioutil.ReadFile(cfgPath)
		if err != nil {
			return params.Project{}, errors.Wrapf(err, "failed to read file %s", cfgPath)
		}
	}
	cfg, err := LoadRawConfig(string(yml), jsonContent)
	if err != nil {
		return params.Project{}, err
	}
	return cfg.ToParams()
}

func LoadRawConfig(ymlContent, jsonContent string) (Project, error) {
	cfg := Project{}
	if ymlContent != "" {
		if err := yaml.Unmarshal([]byte(ymlContent), &cfg); err != nil {
			return Project{}, errors.Wrapf(err, "failed to unmarshal yml %s", ymlContent)
		}
	}
	if jsonContent != "" {
		jsonCfg := Project{}
		if err := json.Unmarshal([]byte(jsonContent), &jsonCfg); err != nil {
			return Project{}, err
		}
		cfg.Exclude.Add(jsonCfg.Exclude)
	}
	return cfg, nil
}
