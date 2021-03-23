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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	apimanifests "github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-sdk/internal/generate/collector"
	"github.com/operator-framework/operator-sdk/internal/util/k8sutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type pkgManToBundleCmd struct {
	pkgmanifestDir string
	buildImg       bool
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

	pkgManToBundleCmd.Flags().BoolVar(&p.buildImg, "build-image", false, "indicates wheather to output bundle as a directory or build an image")
	pkgManToBundleCmd.Flags().StringVar(&p.outputDir, "output-dir", "bundle", "directory to write bundle to.")
	pkgManToBundleCmd.Flags().StringVar(&p.baseImg, "base-image", "", "base container name for bundles")
	pkgManToBundleCmd.Flags().StringVar(&p.buildCmd, "build-cmd", "", "fully qualified build command")

	return pkgManToBundleCmd
}

func (p *pkgManToBundleCmd) run() (err error) {
	if err := p.setDefaults(); err != nil {
		return err
	}

	// move this to validation
	pkgManifest, bundles, err := apimanifests.GetManifestsDir(p.pkgmanifestDir)
	if err != nil {
		return fmt.Errorf("error getting packagemanifests from directory %s %v", p.pkgmanifestDir, err)
	}

	// no neeed to error, just log if bundles or manifests are not present
	if len(bundles) == 0 || pkgManifest.IsEmpty() {
		return fmt.Errorf("no packages found in directory %s", p.pkgmanifestDir)
	}
	fmt.Println("packagename")
	fmt.Println(pkgManifest.PackageName)
	for _, b := range bundles {
		col := &collector.Manifests{}

		ver, err := getVersion(b.Name)
		if err != nil {
			return err
		}

		col.ClusterServiceVersions = make([]v1alpha1.ClusterServiceVersion, 0)
		col.ClusterServiceVersions = append(col.ClusterServiceVersions, *b.CSV)

		fmt.Println("filepathwhere manifests are present")
		fmt.Println(filepath.Join(p.pkgmanifestDir, ver))
		fmt.Println(col.ClusterServiceVersions)
		err = col.UpdateFromDir(filepath.Join(p.pkgmanifestDir, ver))
		if err != nil {
			return err
		}

		dir := filepath.Join(p.outputDir, ver)
		objs := GetManifestObjects(col)
		err = WriteObjectsToFiles(dir, objs...)
		if err != nil {
			return err
		}

	}

	return nil
}

// GetManifestObjects returns all objects to be written to a manifests directory from collector.Manifests.
func GetManifestObjects(c *collector.Manifests) (objs []client.Object) {
	// All CRDs passed in should be written.
	for i := range c.V1CustomResourceDefinitions {
		objs = append(objs, &c.V1CustomResourceDefinitions[i])
	}
	for i := range c.V1beta1CustomResourceDefinitions {
		objs = append(objs, &c.V1beta1CustomResourceDefinitions[i])
	}

	// All ServiceAccounts passed in should be written.
	for i := range c.ServiceAccounts {
		objs = append(objs, &c.ServiceAccounts[i])
	}

	// All Services passed in should be written.
	for i := range c.Services {
		objs = append(objs, &c.Services[i])
	}

	// Add all other supported kinds
	for i := range c.Others {
		obj := &c.Others[i]
		if supported, _ := bundle.IsSupported(obj.GroupVersionKind().Kind); supported {
			objs = append(objs, obj)
		}
	}

	// RBAC objects that are not a part of the CSV should be written.
	_, roleObjs := c.SplitCSVPermissionsObjects()
	objs = append(objs, roleObjs...)
	_, clusterRoleObjs := c.SplitCSVClusterPermissionsObjects()
	objs = append(objs, clusterRoleObjs...)

	removeNamespace(objs)
	return objs
}

// removeNamespace removes the namespace field of resources intended to be inserted into
// an OLM manifests directory.
//
// This is required to pass OLM validations which require that namespaced resources do
// not include explicit namespace settings. OLM automatically installs namespaced
// resources in the same namespace that the operator is installed in, which is determined
// at runtime, not bundle/packagemanifests creation time.
func removeNamespace(objs []client.Object) {
	for _, obj := range objs {
		obj.SetNamespace("")
	}
}

func WriteObjectsToFiles(dir string, objs ...client.Object) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	seenFiles := make(map[string]struct{})
	// Use the number of dupliates in file names so users can debug duplicate file behavior.
	dupCount := 0
	for _, obj := range objs {
		var fileName string
		switch t := obj.(type) {
		case *apiextv1.CustomResourceDefinition:
			if t.Spec.Group != "" && t.Spec.Names.Plural != "" {
				fileName = makeCRDFileName(t.Spec.Group, t.Spec.Names.Plural)
			} else {
				fileName = makeObjectFileName(t)
			}
		case *apiextv1beta1.CustomResourceDefinition:
			if t.Spec.Group != "" && t.Spec.Names.Plural != "" {
				fileName = makeCRDFileName(t.Spec.Group, t.Spec.Names.Plural)
			} else {
				fileName = makeObjectFileName(t)
			}
		default:
			fileName = makeObjectFileName(t)
		}

		if _, hasFile := seenFiles[fileName]; hasFile {
			fileName = fmt.Sprintf("dup%d_%s", dupCount, fileName)
			dupCount++
		}
		if err := writeObjectToFile(dir, obj, fileName); err != nil {
			return err
		}
		seenFiles[fileName] = struct{}{}
	}
	return nil
}

func makeCRDFileName(group, resource string) string {
	return fmt.Sprintf("%s_%s.yaml", group, resource)
}

func makeObjectFileName(obj client.Object) string {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Group == "" {
		return fmt.Sprintf("%s_%s_%s.yaml", obj.GetName(), gvk.Version, strings.ToLower(gvk.Kind))
	}
	return fmt.Sprintf("%s_%s_%s_%s.yaml", obj.GetName(), gvk.Group, gvk.Version, strings.ToLower(gvk.Kind))
}

func writeObjectToFile(dir string, obj interface{}, fileName string) error {
	f, err := os.Create(filepath.Join(dir, fileName))
	if err != nil {
		return err
	}
	defer f.Close()
	return writeObject(f, obj)
}

func getVersion(packageName string) (string, error) {
	reg := regexp.MustCompile("v[0-9]+.[0-9]+.[0-9]+")
	v := reg.FindString(packageName)
	if v == "" {
		return "", fmt.Errorf("cannot find version of the packagemanifest")
	}
	return v[1:], nil
}

// func (*pkgManToBundleCmd) runManifests(bundle *apimanifests.Bundle) (err error) {
// 	col := &collector.Manifests{
// 		ClusterServiceVersions: bundle.CSV,
// 	}

// }

func (p *pkgManToBundleCmd) validate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("a package manifest directory argument is required")
	}
	return nil
}

func (p *pkgManToBundleCmd) setDefaults() (err error) {
	if p.buildCmd == "" {
		p.buildCmd = "docker build -t"
	}

	if p.buildImg && p.baseImg == "" {
		return fmt.Errorf("base image needs to be provided if the output is to be built into a image")
	}
	return nil
}

// writeObject marshals crd to bytes and writes them to w.
func writeObject(w io.Writer, obj interface{}) error {
	b, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// WriteObject writes a k8s object to w.
func WriteObject(w io.Writer, obj interface{}) error {
	b, err := k8sutil.GetObjectBytes(obj, yaml.Marshal)
	if err != nil {
		return err
	}
	return write(w, b)
}

// File wraps os.File. Use this type when generating files that may already
// exist on disk and should be overwritten.
type File struct {
	*os.File
}

// write writes b to w. If w is a File, its contents will be cleared and w
// will be closed following the write.
func write(w io.Writer, b []byte) error {
	if f, isFile := w.(*File); isFile {
		if err := f.Truncate(0); err != nil {
			return err
		}
		defer func() {
			_ = f.Close()
		}()
	}
	_, err := w.Write(b)
	return err
}
