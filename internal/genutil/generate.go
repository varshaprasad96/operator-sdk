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

package genutil

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	"github.com/operator-framework/operator-sdk/internal/util/projutil"
	log "github.com/sirupsen/logrus"
)

var (
	defaultMetadataDir = "metadata"
	defaultManifestDir = "manifests"
)

// values to populate bundle metadata/Dockerfile.
type annotationsValues struct {
	BundleDir      string
	PackageName    string
	Channels       string
	DefaultChannel string
	OtherLabels    []string
}

func (meta *BundleMetaData) GenerateMetadata() error {
	// Create output directory
	if err := os.MkdirAll(meta.BundleDir, projutil.DirMode); err != nil {
		return err
	}

	// Create annotation values for both bundle.Dockerfile and annotations.yaml, which should
	// hold the same set of values always.
	values := annotationsValues{
		BundleDir:      meta.BundleDir,
		PackageName:    meta.PackageName,
		Channels:       meta.Channels,
		DefaultChannel: meta.DefaultChannel,
	}

	for k, v := range meta.OtherLabels {
		values.OtherLabels = append(values.OtherLabels, fmt.Sprintf("%s=%s", k, v))
	}

	// Write each file
	metadataDir := filepath.Join(meta.BundleDir, defaultMetadataDir)
	if err := os.MkdirAll(metadataDir, projutil.DirMode); err != nil {
		return err
	}

	templateMap := map[string]*template.Template{
		filepath.Join(meta.BundleDir, "bundle.Dockerfile"): dockerfileTemplate,
		filepath.Join(metadataDir, "annotations.yaml"):     annotationsTemplate,
	}

	for path, tmpl := range templateMap {
		log.Info(fmt.Sprintf("Creating %s", path))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			return err
		}

		defer func() {
			if err := f.Close(); err != nil {
				log.Error(err)
			}
		}()
		if err = tmpl.Execute(f, values); err != nil {
			return err
		}
	}
	log.Infof("Bundle metadata generated suceessfully")
	return nil
}

func (meta *BundleMetaData) CopyOperatorManifests() error {
	// srcManifestDir := filepath.Join(meta.PkgmanifestPath, defaultManifestDir)
	destManifestDir := filepath.Join(meta.BundleDir, defaultManifestDir)

	return copyOperatorManifests(meta.PkgmanifestPath, destManifestDir)
}

func copyOperatorManifests(src, dest string) error {

	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("error reading source directory %v", err)
	}

	if err := os.MkdirAll(dest, srcInfo.Mode()); err != nil {
		return err
	}

	srcFiles, err := ioutil.ReadDir(src)
	if err != nil {
		return err
	}

	for _, f := range srcFiles {
		srcPath := filepath.Join(src, f.Name())
		destPath := filepath.Join(dest, f.Name())

		if f.IsDir() {
			// TODO(verify): we may have to log an error here instead of recursively copying
			// if there are no subfolders allowed under manifests dir of a packagemanifest.
			if err = copyOperatorManifests(srcPath, destPath); err != nil {
				return err
			}
		} else {
			srcFile, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			destFile, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer destFile.Close()

			_, err = io.Copy(destFile, srcFile)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
