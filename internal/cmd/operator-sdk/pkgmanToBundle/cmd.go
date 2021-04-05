// Copyright 2020 The Operator-SDK Authors
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

package pkgManToBundle

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	semver "github.com/blang/semver/v4"
	apimanifests "github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/operator-sdk/internal/annotations/metrics"
	"github.com/operator-framework/operator-sdk/internal/genutil"
	log "github.com/sirupsen/logrus"

	"github.com/spf13/cobra"
)

type pkgManToBundleCmd struct {
	pkgmanifestDir string
	outputDir      string
	baseImg        string
	buildCmd       string
}

func NewCmd() *cobra.Command {
	p := pkgManToBundleCmd{}

	pkgManToBundleCmd := &cobra.Command{
		Use:   "pkgman-to-bundle",
		Short: "Migrates package manifests to bundle",
		Long:  "",
		PreRunE: func(cmd *cobra.Command, args []string) (err error) {
			return p.validate(args)
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			p.pkgmanifestDir = args[0]
			return p.run()
		},
	}

	pkgManToBundleCmd.Flags().StringVar(&p.outputDir, "output-dir", "", "directory to write bundle to.")
	pkgManToBundleCmd.Flags().StringVar(&p.baseImg, "base-image", "", "base container name for bundles")
	pkgManToBundleCmd.Flags().StringVar(&p.buildCmd, "build-cmd", "", "fully qualified build command")

	return pkgManToBundleCmd
}

func (p *pkgManToBundleCmd) run() (err error) {
	if err := p.setDefaults(); err != nil {
		return err
	}

	// remove the directories if it exists previously
	if _, err := os.Stat(p.outputDir); !os.IsNotExist(err) {
		// TODO(Verify): Do we need an overwrite flag here ?
		log.Infof("%s directory already exists. Overwriting contents.", p.outputDir)
		os.RemoveAll(p.outputDir)
	}

	// Skipping bundles here, since that's not required and could be empty for a manifest directory.
	packages, _, err := apimanifests.GetManifestsDir(p.pkgmanifestDir)
	if err != nil {
		return err
	}

	if packages.IsEmpty() {
		return fmt.Errorf("no packages found in the directory %s", p.pkgmanifestDir)
	}

	packageName, channels, defaultChannel, err := getPackageMetadata(packages)
	if err != nil {
		return fmt.Errorf("error obtaining metadata from directory %s: %v", p.pkgmanifestDir, err)
	}

	directories, err := ioutil.ReadDir(p.pkgmanifestDir)
	if err != nil {
		return err
	}

	for _, dir := range directories {
		if dir.IsDir() {
			if !IsValidSemver(dir.Name()) {
				log.Infof("skipping %s as the directory name is not in the semver format", dir.Name())
				continue
			}

			otherLabels, err := getSDKStamps(filepath.Join(p.pkgmanifestDir, dir.Name()))
			if err != nil {
				return fmt.Errorf("error getting CSV from provided packagemanifest %v", err)
			}

			bundleMetaData := genutil.BundleMetaData{
				BundleDir:       filepath.Join(p.outputDir, "bundle-"+dir.Name()),
				PackageName:     packageName,
				Channels:        channels,
				DefaultChannel:  defaultChannel,
				PkgmanifestPath: filepath.Join(p.pkgmanifestDir, dir.Name()),
				OtherLabels:     otherLabels,
			}

			if err := bundleMetaData.CopyOperatorManifests(); err != nil {
				return err
			}

			if err := bundleMetaData.GenerateMetadata(); err != nil {
				return err
			}

		}
	}
	return nil
}

func getSDKStamps(path string) (map[string]string, error) {
	bundle, err := apimanifests.GetBundleFromDir(path)
	if err != nil {
		return nil, err
	}

	if bundle.CSV == nil {
		return nil, fmt.Errorf("cannot find CSV from manifests package")
	}

	csvAnnotations := bundle.CSV.GetAnnotations()
	sdkLabels := make(map[string]string)

	for key, value := range csvAnnotations {
		if key == metrics.BuilderObjectAnnotation {
			sdkLabels[key] = value
		}

		if key == metrics.LayoutObjectAnnotation {
			sdkLabels[key] = value
		}
	}
	return sdkLabels, nil
}

func getPackageMetadata(pkg *apimanifests.PackageManifest) (packagename, channels, defaultChannel string, err error) {
	packagename = pkg.PackageName
	if packagename == "" {
		err = fmt.Errorf("cannot find packagename from the manifest directory")
		return
	}

	defaultChannel = pkg.DefaultChannelName

	for _, ch := range pkg.Channels {
		channels = ch.Name + ","
	}

	// TODO: verify if we have to add this validation since while building bundles if channel is not specified
	// we add the default channel.
	if channels == "" {
		channels = "alpha"
		log.Infof("supported channels cannot be identified from manifests, hence adding default `alpha` channel")
	} else {
		channels = channels[:len(channels)-1]
	}
	return
}

func IsValidSemver(input string) bool {
	_, err := semver.Parse(input)
	if err != nil {
		return false
	}

	return true
}

func (p *pkgManToBundleCmd) validate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("a package manifest directory argument is required")
	}
	return nil
}

func (p *pkgManToBundleCmd) setDefaults() (err error) {
	if p.outputDir == "" {
		p.outputDir = "bundle"
		log.Infof("packagemanifests will be migrated to bundles in %s directory", p.outputDir)
	}
	return nil
}
